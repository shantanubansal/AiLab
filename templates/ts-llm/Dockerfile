# Local-parity Dockerfile for the ts-llm template. The platform builder
# can rewrite this via Kaniko + buildpacks; checked in so devs can
# `docker build` and `docker run` for local testing.

FROM node:22-alpine AS build
WORKDIR /app
COPY package.json tsconfig.json ./
RUN npm install --no-audit --no-fund
COPY src ./src
RUN npx tsc -p tsconfig.json

FROM node:22-alpine
WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev --no-audit --no-fund
COPY --from=build /app/dist ./dist

RUN addgroup -g 1000 agent && adduser -D -u 1000 -G agent agent
USER 1000

ENTRYPOINT ["node", "dist/agent.js"]
