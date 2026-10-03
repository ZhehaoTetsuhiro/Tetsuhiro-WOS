# Tetsuhiro WOS 容器镜像
#
# WOS 是纯 Go（CGO_ENABLED=0）静态单文件，故镜像只需要二进制 + 元件定义文件。
# 二进制由 .github/workflows/ghcr.yml 用与 Release 完全相同的工具链与标志编译，
# 放在构建上下文根目录的 dist/wos；本地手工构建：
#
#   export CGO_ENABLED=0 GOEXPERIMENT=nodwarf5 GOFLAGS= GOTOOLCHAIN=go1.25.10
#   go build -trimpath -ldflags="-s -w" -o dist/wos ./cmd/wos
#   docker build -f docker/Dockerfile --build-arg VERSION=v1.3.0 \
#     --build-arg REVISION=$(git rev-parse HEAD) -t tetsuhiro-wos:v1.3.0 .
#
# 运行（默认监听 :1120）：
#
#   docker run -p 1120:1120 ghcr.io/zhehaotetsuhiro/tetsuhiro-wos:v1.3.0
#
FROM alpine:3.20

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="Tetsuhiro WOS" \
      org.opencontainers.image.description="波动光学 + 量子光学仿真内核（纯 Go 静态单文件）+ Web GUI" \
      org.opencontainers.image.source="https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS" \
      org.opencontainers.image.url="https://github.com/ZhehaoTetsuhiro/Tetsuhiro-WOS/releases" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

RUN addgroup -S wos && adduser -S -G wos -h /home/wos wos

COPY dist/wos /usr/local/bin/wos
COPY elements /usr/share/wos/elements

USER wos
WORKDIR /home/wos
EXPOSE 1120

ENTRYPOINT ["/usr/local/bin/wos", "-addr", ":1120", "-elements", "/usr/share/wos/elements"]
