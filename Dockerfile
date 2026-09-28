FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /app ./...

FROM scratch
COPY --from=build /app /app
COPY --from=build /src/templates /templates
VOLUME /data
ENV DB_PATH=/data/app.db PORT=8080 TEMPLATES_DIR=/templates
EXPOSE 8080
ENTRYPOINT ["/app"]
