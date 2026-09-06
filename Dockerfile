# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: Apache-2.0
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kairo ./cmd/kairo

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/kairo /usr/local/bin/kairo
COPY examples ./examples
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kairo"]
CMD ["serve", "-addr", ":8080"]
