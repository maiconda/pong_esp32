package lobby

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"pong-server/game"

	"github.com/gorilla/websocket"
)

// Upgrader para converter conexões HTTP em WebSockets
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Permite qualquer origem para facilidade de testes online
	},
}

// Manager coordena as duas conexões de rede ativas e as conecta com o motor de estados
type Manager struct {
	mu          sync.Mutex
	PlayerLeft  *websocket.Conn
	PlayerRight *websocket.Conn
	Engine      *game.Engine
}

// NewManager cria e configura um gerenciador de lobby integrado com a engine física
func NewManager(engine *game.Engine) *Manager {
	return &Manager{
		Engine: engine,
	}
}

// SetupMessage define o JSON de boas-vindas do jogador
type SetupMessage struct {
	Type string `json:"type"`
	Side string `json:"side"`
}

// ClientMessage representa o formato unificado de inputs recebidos do cliente (ESP32)
type ClientMessage struct {
	Type string  `json:"type"`
	Dir  float64 `json:"dir"` // Utilizado quando Type == "input" (movimento)
}

// HandleWS processa handshakes de novos clientes na rota /ws
func (m *Manager) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Erro ao realizar upgrade da conexao: %v\n", err)
		return
	}

	m.mu.Lock()
	var assignedSide string

	// Registra o cliente na vaga livre
	if m.PlayerLeft == nil {
		m.PlayerLeft = conn
		assignedSide = "left"
		fmt.Println("Lobby: Jogador 1 (Esquerda) conectado.")
	} else if m.PlayerRight == nil {
		m.PlayerRight = conn
		assignedSide = "right"
		fmt.Println("Lobby: Jogador 2 (Direita) conectado.")
	} else {
		m.mu.Unlock()
		fmt.Println("Lobby recusado: Ambas as vagas estao cheias.")
		errPayload, _ := json.Marshal(map[string]string{
			"type":  "error",
			"error": "lobby_full",
		})
		conn.WriteMessage(websocket.TextMessage, errPayload)
		conn.Close()
		return
	}

	// Calcula quantidade de jogadores ativos e avisa a engine
	count := 0
	if m.PlayerLeft != nil {
		count++
	}
	if m.PlayerRight != nil {
		count++
	}
	m.Engine.PlayerConnected(count)
	m.mu.Unlock()

	// Envia mensagem de configuração inicial informando qual lado ele assumiu
	setupMsg := SetupMessage{
		Type: "setup",
		Side: assignedSide,
	}
	if payload, err := json.Marshal(setupMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, payload)
	}

	// Inicia a escuta de dados recebidos por este cliente em uma goroutine separada
	go m.readLoop(conn, assignedSide)
}

// readLoop processa os pacotes recebidos de cada cliente ativo
func (m *Manager) readLoop(conn *websocket.Conn, side string) {
	defer func() {
		conn.Close()

		m.mu.Lock()
		if side == "left" {
			m.PlayerLeft = nil
			fmt.Println("Lobby: Jogador 1 (Esquerda) desconectado.")
		} else if side == "right" {
			m.PlayerRight = nil
			fmt.Println("Lobby: Jogador 2 (Direita) desconectado.")
		}
		
		// Atualiza a engine física sobre a desconexão de um dos lados
		m.Engine.PlayerDisconnected()
		m.mu.Unlock()
	}()

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			break // Sai do loop para acionar o defer e desconectar o cliente
		}

		// Processa mensagens
		var msg ClientMessage
		if err := json.Unmarshal(payload, &msg); err == nil {
			if msg.Type == "input" {
				// Recebeu comando de movimento de raquete
				m.Engine.SetPaddleDir(side, msg.Dir)
			} else if msg.Type == "ready" {
				// Recebeu solicitação para alternar o status de pronto
				m.Engine.ToggleReady(side)
			}
		}
	}
}

// StartGameLoop roda a 30 FPS computando as regras e atualizando ambos os clientes em tempo real
func (m *Manager) StartGameLoop() {
	ticker := time.NewTicker(33 * time.Millisecond) // ~30 FPS
	defer ticker.Stop()

	for range ticker.C {
		// 1. Atualiza regras físicas da bola e raquetes na engine
		m.Engine.Update()

		m.mu.Lock()
		left := m.PlayerLeft
		right := m.PlayerRight
		m.mu.Unlock()

		// 2. Se houver pelo menos um jogador conectado, transmite o novo JSON de estado
		if left != nil || right != nil {
			payload, err := m.Engine.GetStateJSON()
			if err == nil {
				if left != nil {
					if err := left.WriteMessage(websocket.TextMessage, payload); err != nil {
						// Ignora erro aqui, a desconexão será tratada automaticamente pelo loop de leitura
					}
				}
				if right != nil {
					if err := right.WriteMessage(websocket.TextMessage, payload); err != nil {
						// Ignora erro aqui
					}
				}
			}
		}
	}
}
