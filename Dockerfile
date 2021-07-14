FROM golang:1.15.11 AS build

ENV GOPROXY="https://goproxy.cn"
ENV GO111MODULE="on"

COPY . /root/build
WORKDIR /root/build

RUN set -ex && echo "Asia/Shanghai" >/etc/timezone

RUN cd filebeat && make

FROM registry.cn-hangzhou.aliyuncs.com/terminus/terminus-centos:base

WORKDIR /app

ENV GODEBUG="madvdontneed=1"

COPY --from=build /root/build/entrypoint.sh /app/
COPY --from=build /root/build/filebeat/filebeat /app/
COPY --from=build /root/build/filebeat/conf/* /app/conf/

CMD ["./entrypoint.sh"]
