# syntax=docker/dockerfile:1
FROM golang:1.21 AS build
WORKDIR /src
# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading
# them in subsequent builds if they change.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . ./
# -ldflags to reduce binary size.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -ldflags '-w -s' -o /usr/local/bin/app .

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=build /usr/local/bin/app .
ENTRYPOINT ["./app"]
