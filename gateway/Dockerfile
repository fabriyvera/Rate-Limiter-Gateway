FROM golang:1.26.4 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd/gateway && CGO_ENABLED=0 go build -o /out/upstream ./cmd/upstream
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /
ENTRYPOINT ["/gateway"]
