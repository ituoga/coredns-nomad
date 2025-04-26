FROM golang:1.23-alpine
RUN apk update && apk add make git

WORKDIR /app
RUN git clone https://github.com/coredns/coredns
COPY . /coredns-nomad
RUN cp /coredns-nomad/plugin.cfg coredns/plugin.cfg

WORKDIR /app/coredns
RUN go mod edit -replace github.com/ituoga/coredns-nomad=/coredns-nomad
RUN go mod download

RUN make gen 
RUN make coredns

FROM scratch
COPY --from=0 /app/coredns /

EXPOSE 53

ENTRYPOINT ["/coredns"]
