FROM golang:1.25.1-alpine3.21 AS builder

WORKDIR /app

COPY go.mod go.su[m] ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /go-cek-gigs ./main.go

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=builder /go-cek-gigs /go-cek-gigs

EXPOSE 8080
ENTRYPOINT ["/go-cek-gigs"]
