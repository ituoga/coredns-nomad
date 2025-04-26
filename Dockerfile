FROM golang:1.23-alpine
RUN apk update && apk add make git
RUN go env -w GOMODCACHE=/root/.cache/go-build
WORKDIR /app
RUN git clone https://github.com/coredns/coredns
COPY . /coredns-nomad
RUN cp /coredns-nomad/plugin.cfg coredns/plugin.cfg

WORKDIR /app/coredns
RUN go mod edit -replace github.com/ituoga/coredns-nomad=/coredns-nomad
RUN --mount=type=cache,target=/root/.cache/go-build go mod download

RUN --mount=type=cache,target=/root/.cache/go-build make gen 
RUN --mount=type=cache,target=/root/.cache/go-build make coredns

FROM scratch
COPY --from=0 /app/coredns /

EXPOSE 53

ENTRYPOINT ["/coredns"]
