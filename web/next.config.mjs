/** @type {import('next').NextConfig} */
const nextConfig = {
  // The api lives on a different origin in dev (:8080). We don't proxy
  // here — the api enables CORS for http://localhost:3000.
  reactStrictMode: true,
};

export default nextConfig;
