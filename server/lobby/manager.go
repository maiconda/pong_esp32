package lobby

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Upgrader para converter requisições HTTP normais em conexões WebSocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Permite conexões de qualquer origem para facilitar testes locais
	},
}

// Manager gerencia as duas vagas de jogadores ativos no WebSocket
type Manager struct {
	mu          sync.Mutex
	PlayerLeft  *websocket.Conn
	PlayerRight *websocket.Conn
}

// NewManager cria uma nova instância do gerenciador de lobby
func NewManager() *Manager {
	return &Manager{}
}

// SetupMessage define a mensagem JSON de configuração enviada ao cliente
type SetupMessage struct {
	Type string `json:"type"`
	Side string `json:"side"`
}

// HandleWS processa conexões de WebSocket no endpoint /ws
func (m *Manager) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Erro ao fazer o upgrade da conexao: %v\n", err)
		return
	}

	m.mu.Lock()
	var assignedSide string

	// Tenta ocupar a vaga da esquerda (Player 1)
	if m.PlayerLeft == nil {
		m.PlayerLeft = conn
		assignedSide = "left"
		fmt.Println("Jogador 1 (Esquerda) conectado!")
	} else if m.PlayerRight == nil { // Se ocupado, tenta ocupar a vaga da direita (Player 2)
		m.PlayerRight = conn
		assignedSide = "right"
		fmt.Println("Jogador 2 (Direita) conectado!")
	} else { // Se ambas estiverem cheias, rejeita
		m.mu.Unlock()
		fmt.Println("Conexao recusada: Lobby cheio (max 2 jogadores).")
		errPayload, _ := json.Marshal(map[string]string{
			"type":  "error",
			"error": "lobby_full",
		})
		conn.WriteMessage(websocket.TextMessage, errPayload)
		conn.Close()
		return
	}
	m.mu.Unlock()

	// Envia a resposta de boas-vindas com o lado atribuído
	setupMsg := SetupMessage{
		Type: "setup",
		Side: assignedSide,
	}
	if payload, err := json.Marshal(setupMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, payload)
	}

	// Mantém a conexão aberta em uma goroutine escutando desconexões
	go m.readLoop(conn, assignedSide)
}

// readLoop monitora as mensagens e detecta desconexão
func (m *Manager) readLoop(conn *websocket.Conn, side string) {
	defer func() {
		conn.Close()
		m.mu.Lock()
		if side == "left" {
			m.PlayerLeft = nil
			fmt.Println("Jogador 1 (Esquerda) se desconectou.")
		} else if side == "right" {
			m.PlayerRight = nil
			fmt.Println("Jogador 2 (Direita) se desconectou.")
		}
		m.mu.Unlock()
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			// Erro na leitura indica desconexão do cliente
			break
		}
	}
}
