FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/homelab-inventory ./cmd/homelab-inventory

# Alpine (not distroless): the shared release pipeline validates each arch with
# `docker run <image> uname -m`, which needs a runnable command in the image.
FROM alpine:3.24
RUN apk upgrade --no-cache && adduser -D -u 65532 app
COPY --from=build /out/homelab-inventory /usr/local/bin/homelab-inventory
USER app
# 8080: public, accepts reports only. 8081: internal, serves the inventory.
EXPOSE 8080 8081
CMD ["homelab-inventory"]
