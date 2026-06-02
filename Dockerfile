FROM golang:1.22-alpine AS build
LABEL stage=builder

WORKDIR /app

COPY ./source/go.mod ./
COPY ./source/go.sum ./
RUN go mod download

COPY ./source/ ./

RUN go build -o /poker-bot .

FROM alpine:latest

WORKDIR /

RUN apk add --no-cache ca-certificates

COPY --from=build /poker-bot /poker-bot

EXPOSE 8080

CMD ["/poker-bot"]

