FROM golang:alpine
RUN apk add git
RUN mkdir /src
COPY file /src
WORKDIR /src
CMD ["/.main"]
