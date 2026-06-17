# Stage 1: Build the Go application
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy dependencies manifest
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy source code files
COPY backend/ ./

# Compile binary securely for alpine
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o secretary .

# Stage 2: Final runtime image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy compiled binary from build stage
COPY --from=builder /app/secretary .

# Copy frontend to be served statically
COPY frontend/ ./frontend/

EXPOSE 8000

CMD ["./secretary"]
