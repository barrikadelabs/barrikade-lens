# syntax=docker/dockerfile:1.7
FROM node:24-alpine AS ui
WORKDIR /src
COPY package.json package-lock.json ./
COPY hub-ui/package.json hub-ui/tsconfig.json hub-ui/vite.config.ts hub-ui/index.html ./hub-ui/
COPY hub-ui/src ./hub-ui/src
COPY npm/launcher/package.json ./npm/launcher/package.json
RUN npm ci --ignore-scripts && npm run build -w @barrikade/lens-hub-ui

FROM golang:1.26-alpine AS go-build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=2.0.0-dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/lens-hub ./cmd/lens-hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/lens-hub /usr/local/bin/lens-hub
COPY --from=ui /src/hub-ui/dist /app/ui
ENV LENS_LISTEN=:8080 LENS_UI_DIR=/app/ui
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/lens-hub"]
