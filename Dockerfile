FROM golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o mcp-gateway .

FROM python:3.11-slim
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends \
    libc6 sqlite3 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/mcp-gateway .
COPY --from=builder /app/examples/docs-server examples/docs-server
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/render.yaml .
RUN pip install --no-cache-dir flask chromadb pdfplumber
EXPOSE 8080
CMD ["./mcp-gateway"]
