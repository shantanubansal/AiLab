// Command gateway is the public ingress for long-running agents
// (mode=server / MCP). It parses the request Host header into
// <agent>.<tenant>.<configured-suffix>, looks up the AgentDeployment via
// the k8s API, and reverse-proxies to the matching Service in the
// tenant namespace.
//
// Routing examples (with GATEWAY_DOMAIN="run.local"):
//   Host: myagent.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.run.local
//     → tenant=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
//     → agent=myagent
//     → proxy http://myagent.tenant-aaaa....svc.cluster.local:<port>
//
// In-cluster the proxy target resolves via service DNS. Outside the
// cluster (make run-gateway) the gateway uses the kubernetes API server's
// /api/v1/namespaces/<ns>/services/<svc>:<port>/proxy/ URL space.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var agentDeploymentGVR = schema.GroupVersionResource{
	Group:    "ailab.uipath.com",
	Version:  "v1alpha1",
	Resource: "agentdeployments",
}

func main() {
	listenAddr := envOr("GATEWAY_LISTEN_ADDR", ":8083")
	domain := envOr("GATEWAY_DOMAIN", "run.local")
	useAPIProxy := envOr("GATEWAY_USE_K8S_PROXY", "auto") // auto | true | false

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, inCluster, err := loadKubeConfig()
	if err != nil {
		log.Fatalf("kube config: %v", err)
	}
	_ = inCluster

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("dynamic client: %v", err)
	}

	apiProxy := false
	switch useAPIProxy {
	case "true":
		apiProxy = true
	case "false":
		apiProxy = false
	default: // auto: prefer API-server proxy outside the cluster
		apiProxy = !inCluster
	}

	apiHTTP, err := rest.HTTPClientFor(cfg)
	if err != nil {
		log.Fatalf("rest http client: %v", err)
	}
	apiHost, err := url.Parse(cfg.Host)
	if err != nil {
		log.Fatalf("parse cfg.Host: %v", err)
	}

	g := &gateway{
		domain:     domain,
		dyn:        dyn,
		cache:      newCache(5 * time.Second),
		useAPIProxy: apiProxy,
		apiHost:    apiHost,
		apiClient:  apiHTTP,
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           g,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("gateway listening on %s (domain=%s, apiProxy=%v)", listenAddr, domain, apiProxy)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-rootCtx.Done()
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(ctxShutdown)
}

type gateway struct {
	domain      string
	dyn         dynamic.Interface
	cache       *cache
	useAPIProxy bool
	apiHost     *url.URL
	apiClient   *http.Client
}

// resolved is what the gateway caches per Host.
type resolved struct {
	tenantID  string
	namespace string
	service   string
	port      int32
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	agent, tenant, ok := parseHost(host, g.domain)
	if !ok {
		http.Error(w, "bad host: expected <agent>.<tenant>."+g.domain, http.StatusBadGateway)
		return
	}

	res, err := g.resolve(r.Context(), agent, tenant)
	if err != nil {
		log.Printf("resolve %s.%s: %v", agent, tenant, err)
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}

	target := g.targetURL(res)
	prox := httputil.NewSingleHostReverseProxy(target)
	if g.useAPIProxy {
		prox.Transport = g.apiClient.Transport
		base := strings.TrimSuffix(target.Path, "/")
		origDirector := prox.Director
		prox.Director = func(req *http.Request) {
			origDirector(req)
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = base + req.URL.Path
			req.Host = target.Host
		}
	}
	prox.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		log.Printf("proxy error to %s: %v", target.String(), err)
		http.Error(w, "upstream error", http.StatusBadGateway)
	}
	prox.ServeHTTP(w, r)
}

func (g *gateway) resolve(ctx context.Context, agent, tenant string) (resolved, error) {
	if v, ok := g.cache.get(agent + "." + tenant); ok {
		return v, nil
	}
	ns := "tenant-" + tenant
	obj, err := g.dyn.Resource(agentDeploymentGVR).Namespace(ns).Get(ctx, agent, metav1.GetOptions{})
	if err != nil {
		return resolved{}, err
	}
	spec, _, _ := unstructuredMap(obj.Object, "spec")
	portFloat, _ := spec["port"].(float64)
	if v, ok := spec["port"].(int64); ok {
		portFloat = float64(v)
	}
	res := resolved{
		tenantID:  tenant,
		namespace: ns,
		service:   agent,
		port:      int32(portFloat),
	}
	if res.port == 0 {
		res.port = 8080
	}
	g.cache.put(agent+"."+tenant, res)
	return res, nil
}

func (g *gateway) targetURL(r resolved) *url.URL {
	if g.useAPIProxy {
		u := *g.apiHost
		u.Path = fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%d/proxy/", r.namespace, r.service, r.port)
		return &u
	}
	return &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", r.service, r.namespace, r.port),
	}
}

// parseHost takes "<agent>.<tenant>.<domain>" and returns agent, tenant.
// domain may have multiple labels, e.g. "run.local" or "run.example.com".
func parseHost(host, domain string) (string, string, bool) {
	suffix := "." + domain
	if !strings.HasSuffix(host, suffix) {
		return "", "", false
	}
	prefix := strings.TrimSuffix(host, suffix)
	parts := strings.SplitN(prefix, ".", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func unstructuredMap(in map[string]any, key string) (map[string]any, bool, error) {
	v, ok := in[key]
	if !ok {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%s is not a map", key)
	}
	return m, true, nil
}

// loadKubeConfig prefers in-cluster, then KUBECONFIG.
func loadKubeConfig() (*rest.Config, bool, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, true, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, false, err
	}
	return cfg, false, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// cache is a tiny TTL'd map from host → resolved.
type cache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cacheEntry
}

type cacheEntry struct {
	val    resolved
	expiry time.Time
}

func newCache(ttl time.Duration) *cache { return &cache{ttl: ttl, m: make(map[string]cacheEntry)} }

func (c *cache) get(k string) (resolved, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Now().After(e.expiry) {
		return resolved{}, false
	}
	return e.val, true
}

func (c *cache) put(k string, v resolved) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = cacheEntry{val: v, expiry: time.Now().Add(c.ttl)}
}
