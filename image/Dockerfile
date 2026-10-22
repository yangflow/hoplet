# The one image: the built web UI served by the core. Builds linux/arm64 and linux/amd64.
# The Node.js major agrees with .node-version (26.8.2).
FROM node:26-bookworm-slim@sha256:298992629d69bbd5b2b03ad57094d8e4fcb3a8eeb7c4e25d29de656519568913 AS build
WORKDIR /app
RUN npm install --global pnpm@12.9.1
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY core/package.json core/
COPY shared/package.json shared/
COPY web/package.json web/
COPY sdk/package.json sdk/
COPY cli/package.json cli/
RUN pnpm install --frozen-lockfile
# Build the web UI, then drop the build tools: the runtime keeps production dependencies only.
COPY shared/src shared/src
COPY core/src core/src
COPY web web
RUN pnpm --filter @hoplet/web build \
 && pnpm install --frozen-lockfile --prod

FROM node:26-bookworm-slim@sha256:298992629d69bbd5b2b03ad57094d8e4fcb3a8eeb7c4e25d29de656519568913
ENV NODE_ENV=production HOPLET_DATA=/data HOPLET_WEB_DIR=/app/web/dist
WORKDIR /app
COPY --from=build /app /app
# Fixed non-root user; /data and /tmp are the only writable paths (compose.yaml enforces the rest).
RUN groupadd --gid 10001 hoplet && useradd --uid 10001 --gid 10001 --no-create-home hoplet \
 && mkdir -p /data && chown hoplet:hoplet /data
USER 10001:10001
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["node", "core/src/health.ts"]
ENTRYPOINT ["node", "core/src/main.ts"]
