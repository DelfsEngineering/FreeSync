# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/freesync ./cmd/freesync

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/freesync /app/freesync

EXPOSE 8080
ENTRYPOINT ["/app/freesync"]
CMD ["serve", "-listen", ":8080"]
