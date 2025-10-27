# Multi-stage build: build plugin .so then run with mosquitto
FROM golang:1.22-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libmosquitto-dev ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod .
RUN go mod download
COPY . .
RUN make build bcryptgen

FROM eclipse-mosquitto:2
# Copy plugin and example config into the image
COPY --from=build /src/build/mosq_pg_auth.so /mosquitto/plugins/mosq_pg_auth.so
COPY mosquitto.conf /mosquitto/config/mosquitto.conf
EXPOSE 1883
CMD ["/docker-entrypoint.sh", "/usr/sbin/mosquitto", "-c", "/mosquitto/config/mosquitto.conf"]
