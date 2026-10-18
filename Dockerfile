FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -S relay -g 10001 \
    && adduser -S relay -u 10001 -G relay \
    && mkdir -p /app /data \
    && chown -R relay:relay /app /data

WORKDIR /app
COPY simple-relay /usr/local/bin/simple-relay
RUN ln -s /usr/local/bin/simple-relay /usr/local/bin/relay

ENV RELAY_DB_PATH=/data/relay.db

USER 10001:10001
EXPOSE 8080

ENTRYPOINT ["simple-relay"]
