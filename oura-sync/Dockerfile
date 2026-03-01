FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/oura-sync ./cmd/oura-sync

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /bin/oura-sync /usr/local/bin/oura-sync
ENTRYPOINT ["oura-sync"]
