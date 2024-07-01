# syntax=docker/dockerfile:1
FROM golang:1.22 AS build
WORKDIR /src
# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading
# them in subsequent builds if they change.
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . ./
# -ldflags to reduce binary size.
# `-tags lambda.norpc` to reduce binary size: https://docs.aws.amazon.com/lambda/latest/dg/go-image.html#go-image-provided
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -tags lambda.norpc -ldflags '-w -s' -o /usr/local/bin/app .

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=build /usr/local/bin/app .
ENTRYPOINT ["./app"]
