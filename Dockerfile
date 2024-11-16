FROM golang:1.22-bullseye AS builder

RUN mkdir /real-history-bot
COPY ./ /real-history-bot/
WORKDIR /real-history-bot
RUN go build \
    && chmod 755 real-history-bot

FROM gcr.io/distroless/base-debian12

COPY --from=builder /real-history-bot/real-history-bot /app/real-history-bot
USER 1001
ENTRYPOINT ["/app/real-history-bot"]
