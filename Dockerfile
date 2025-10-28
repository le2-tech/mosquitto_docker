# Multi-stage build: build plugin .so then run with mosquitto
FROM golang:latest AS build

# 必要开发包：插件头文件在 mosquitto-dev；pkg-config 供 cgo 找编译参数
RUN set -eux; \
    apt-get update ;\
    apt-get install -y --no-install-recommends \
      build-essential pkg-config ca-certificates \
      libmosquitto-dev mosquitto-dev \
    ; \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod .
RUN go mod download
COPY . .
RUN make build-prod


# https://packages.debian.org/search?keywords=mosquitto
FROM debian:sid

RUN set -eux; \
    apt-get update ;\
    apt-get install -y --no-install-recommends \
      mosquitto \
    ; \
    rm -rf /var/lib/apt/lists/*


RUN set -eux; \
    install -d -o mosquitto -g mosquitto -m 755 \
      /mosquitto/config /mosquitto/data /mosquitto/log /mosquitto/plugins

# Copy plugin and example config into the image
COPY --from=build --chown=mosquitto:mosquitto /src/build/ /mosquitto/plugins/

#COPY docker-entrypoint.sh /
#ENTRYPOINT ["/docker-entrypoint.sh"]
#EXPOSE 1883

USER mosquitto

CMD ["mosquitto"]