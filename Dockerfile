FROM golang:1.18 AS build

WORKDIR /app

COPY go.sum .
COPY go.mod .

RUN go mod download

COPY . .

RUN go build -o ingress-controller && chmod +x ./ingress-controller

FROM nginx:1.23

COPY --from=build /app/ingress-controller /ingress-controller

ENTRYPOINT ["/ingress-controller"]