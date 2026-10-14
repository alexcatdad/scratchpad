FROM node:24.21.0-bookworm-slim AS build
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends python3 make g++ && rm -rf /var/lib/apt/lists/*
COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/package.json
COPY apps/docs/package.json apps/docs/package.json
COPY packages/clients/typescript/package.json packages/clients/typescript/package.json
COPY packages/clients/generator/package.json packages/clients/generator/package.json
RUN npm ci
COPY apps/web apps/web
RUN npm run build -w @scratchpad/web

FROM node:24.21.0-bookworm-slim AS runtime
ENV NODE_ENV=production PORT=3000 SCRATCHPAD_DATABASE_PATH=/data/scratchpad.sqlite
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends openssh-client && rm -rf /var/lib/apt/lists/* \
    && mkdir -m 0700 /data && chown node:node /data
COPY --from=build --chown=node:node /app/node_modules ./node_modules
COPY --from=build --chown=node:node /app/apps/web ./apps/web
COPY --from=build --chown=node:node /app/package.json ./package.json
USER 1000:1000
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["node", "-e", "fetch('http://localhost:3000/ready').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"]
CMD ["npm", "run", "start", "-w", "@scratchpad/web"]
