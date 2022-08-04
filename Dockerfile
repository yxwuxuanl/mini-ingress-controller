ARG GO_VERSION=1.18

FROM golang:${GO_VERSION} AS build

WORKDIR /app

COPY go.sum .
COPY go.mod .

RUN go mod download

COPY . .

RUN go build -o ingress-controller && chmod +x ./ingress-controller

ARG NGX_VERSION=1.23

FROM nginx:${NGX_VERSION}

COPY --from=build /app/ingress-controller /ingress-controller

ENTRYPOINT ["/ingress-controller"]