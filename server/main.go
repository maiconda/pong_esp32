package main

import (
	"fmt"
	"net/http"
	"pong-server/lobby"
)

func main() {
	// 1. Instancia o gerenciador do lobby WebSocket
	manager := lobby.NewManager()

	// 2. Rota para o handshake do WebSocket
	http.HandleFunc("/ws", manager.HandleWS)

	// 3. Rota simples de status/healthcheck para a API
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Pong Game Server API - Connect to /ws via WebSocket")
	})

	// 4. Inicia o servidor HTTP
	fmt.Println("Servidor Pong rodando na porta 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Erro ao iniciar o servidor: %v\n", err)
	}
}
