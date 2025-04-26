FROM golang:1.23-alpine
RUN apk update && apk add make git

WORKDIR /app
RUN git clone https://github.com/coredns/coredns
COPY . /coredns-nomad
RUN echo "nomad:github.com/ituoga/coredns-nomad" >> coredns/plugin.cfg

WORKDIR /app/coredns
RUN go mod edit -replace github.com/ituoga/coredns-nomad=/coredns-nomad
RUN --mount=type=cache,target=/root/go go mod download

RUN --mount=type=cache,target=/root/go make gen coredns

FROM scratch
COPY --from=0 /app/coredns /

EXPOSE 53

ENTRYPOINT ["/coredns"]
