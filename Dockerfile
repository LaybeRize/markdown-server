FROM golang:1.24.0-alpine

WORKDIR /app
COPY ./main.go .
COPY ./go.mod .

RUN go mod tidy

RUN go build -o /markdownServer .

EXPOSE 8080

CMD ["/markdownServer"]