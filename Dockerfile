FROM golang:alpine AS builder

# Install git.
RUN apk update && apk add --no-cache git
WORKDIR $GOPATH/src/mypackage/myapp/
COPY . .

# deps
RUN go get -d -v

# Build the binary.
RUN CGO_ENABLED=0 go build -o /go/bin/demo-parser

# STEP 2 build a small image
FROM scratch

# Copy the certs from the builder stage
COPY --from=0 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
# Copy our static executable.
COPY --from=builder /go/bin/demo-parser /go/bin/demo-parser

# Run the demo-parser binary.
ENTRYPOINT ["/go/bin/demo-parser"]
