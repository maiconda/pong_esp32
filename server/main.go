package main

import (
	"fmt"
	"net/http"
	"pong-server/game"
	"pong-server/lobby"
)

func main() {
	// 1. Inicializa o motor físico e a máquina de estados avançada do jogo
	engine := game.NewEngine()

	// 2. Inicializa o lobby e gerenciador de conexões WebSocket injetando a engine
	manager := lobby.NewManager(engine)

	// 3. Inicia o loop físico a 30 FPS do jogo em uma goroutine em background
	go manager.StartGameLoop()

	// 4. Mapeia a rota para o handshake do WebSocket dos controles (ESP32)
	http.HandleFunc("/ws", manager.HandleWS)

	// 5. Rota simples de status/healthcheck para a API
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Pong Game Server API - Connect to /ws via WebSocket (Lobby Machine Active)")
	})

	// 6. Inicia o servidor na porta 8080
	fmt.Println("Servidor Pong rodando na porta 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Erro ao iniciar o servidor: %v\n", err)
	}
}
