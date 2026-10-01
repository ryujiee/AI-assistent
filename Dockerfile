# Stage 1: Build the panel (Vite + Vue 3)
FROM node:24-alpine AS web

WORKDIR /web

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

# Stage 2: Build the Go application
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy dependencies manifest
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy source code files
COPY backend/ ./

# Compile binary securely for alpine
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o secretary .

# Stage 3: Final runtime image (unprivileged user)
FROM alpine:3.22

RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -H -u 10001 secretary

WORKDIR /app

# Copy compiled binary from build stage
COPY --from=builder /app/secretary .

# Copy the built panel, served statically by the Go server
COPY --from=web /web/dist/ ./frontend/

USER secretary

EXPOSE 8000

CMD ["./secretary"]
