# Estágio de build
FROM golang:1.20-alpine AS builder

WORKDIR /app

# Copia os arquivos do módulo
COPY go.mod ./
# (Descomente se tiver go.sum no futuro)
# COPY go.sum ./

RUN go mod download

# Copia o código fonte
COPY . .

# Compila o binário estático
RUN CGO_ENABLED=0 GOOS=linux go build -o pong-server .

# Estágio final de execução
FROM alpine:latest

WORKDIR /app

# Copia o binário compilado do estágio anterior
COPY --from=builder /app/pong-server .

# Expõe a porta do servidor
EXPOSE 8080

# Comando para rodar a aplicação
CMD ["./pong-server"]
