# The web UI: static files served by nginx (unprivileged, port 8080). Build from the ui folder:
#   docker build -t athena-ui ui
FROM node:22.23.1-alpine@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2 AS build
WORKDIR /ui
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY index.html vite.config.js ./
COPY public public
COPY src src
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27.3-alpine@sha256:9e7238f579a54582263a960d1b0094b4a3ecce641342eda3f8e2ff82b1703d2b
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /ui/dist /usr/share/nginx/html
EXPOSE 8080
