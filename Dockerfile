FROM golang:1.24.0-alpine

WORKDIR /app
COPY ./main.go .
COPY ./markdown ./markdown
COPY ./chroma ./chroma
COPY ./regexp2 ./regexp2
COPY ./go.mod .

RUN go build -o /markdownServer .

EXPOSE 8080

CMD ["/markdownServer"]