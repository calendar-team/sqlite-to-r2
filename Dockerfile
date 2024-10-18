FROM golang:1.23 AS build

WORKDIR /go/src/app
COPY . .

RUN go mod download
RUN go vet -v
RUN go test -v

RUN go build -o /go/bin/sqlite-to-r2

FROM gcr.io/distroless/static-debian12

COPY --from=build /go/bin/sqlite-to-r2 /
CMD ["/sqlite-to-r2"]
