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

// Prazos limites para detecção de conexões fantasmas
const (
	writeWait  = 2 * time.Second  // Tempo máximo para tentar escrever uma mensagem
	pongWait   = 10 * time.Second // Tempo limite esperando a resposta do Pong
	pingPeriod = 4 * time.Second  // Frequência de envio de pings (deve ser menor que pongWait)
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Manager coordena conexões de rede ativas com suporte a detecção de zumbis via Ping/Pong
type Manager struct {
	mu          sync.Mutex
	PlayerLeft  *websocket.Conn
	PlayerRight *websocket.Conn
	Engine      *game.Engine
	tickCount   int // Contador de ticks para o controle de Ping em background
}

// NewManager cria e configura uma nova instância do gerenciador de lobby
func NewManager(engine *game.Engine) *Manager {
	return &Manager{
		Engine: engine,
	}
}

type SetupMessage struct {
	Type string `json:"type"`
	Side string `json:"side"`
}

type ClientMessage struct {
	Type string  `json:"type"`
	Dir  float64 `json:"dir"`
}

// HandleWS processa conexões de WebSocket e define as políticas de timeouts e pings
func (m *Manager) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("Erro ao realizar upgrade da conexao: %v\n", err)
		return
	}

	// Define políticas iniciais de leitura para detecção de queda
	conn.SetReadLimit(512) // Limita o tamanho do pacote para segurança
	conn.SetReadDeadline(time.Now().Add(pongWait))
	
	// Sempre que o cliente responder ao nosso "Ping" com um "Pong", renovamos a data limite de leitura
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	m.mu.Lock()
	var assignedSide string

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

	count := 0
	if m.PlayerLeft != nil {
		count++
	}
	if m.PlayerRight != nil {
		count++
	}
	m.Engine.PlayerConnected(count)
	m.mu.Unlock()

	setupMsg := SetupMessage{
		Type: "setup",
		Side: assignedSide,
	}
	if payload, err := json.Marshal(setupMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, payload)
	}

	go m.readLoop(conn, assignedSide)
}

// readLoop processa pacotes do cliente e renova prazos de leitura
func (m *Manager) readLoop(conn *websocket.Conn, side string) {
	defer func() {
		conn.Close()

		m.mu.Lock()
		if side == "left" && m.PlayerLeft == conn {
			m.PlayerLeft = nil
			fmt.Println("Lobby: Jogador 1 (Esquerda) desconectado da vaga.")
		} else if side == "right" && m.PlayerRight == conn {
			m.PlayerRight = nil
			fmt.Println("Lobby: Jogador 2 (Direita) desconectado da vaga.")
		}
		
		m.Engine.PlayerDisconnected()
		m.mu.Unlock()
	}()

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			// Se estourar o deadline de leitura ou houver queda, quebra o loop e desconecta
			break
		}

		// A cada mensagem recebida com sucesso, renovamos o prazo limite de leitura
		conn.SetReadDeadline(time.Now().Add(pongWait))

		var msg ClientMessage
		if err := json.Unmarshal(payload, &msg); err == nil {
			if msg.Type == "input" {
				m.Engine.SetPaddleDir(side, msg.Dir)
			} else if msg.Type == "ready" {
				m.Engine.ToggleReady(side)
			}
		}
	}
}

// StartGameLoop roda a 30 FPS atualizando a física e enviando pings periódicos para expurgar conexões zumbis
func (m *Manager) StartGameLoop() {
	ticker := time.NewTicker(33 * time.Millisecond) // ~30 FPS
	defer ticker.Stop()

	for range ticker.C {
		// 1. Atualiza engine física
		m.Engine.Update()

		// 2. Lógica periódica de envio de Pings (Heartbeat) a cada 4 segundos (~120 ticks)
		m.tickCount++
		if m.tickCount >= 120 {
			m.tickCount = 0
			m.sendPings()
		}

		m.mu.Lock()
		left := m.PlayerLeft
		right := m.PlayerRight
		m.mu.Unlock()

		// 3. Transmite o novo JSON de estado
		if left != nil || right != nil {
			payload, err := m.Engine.GetStateJSON()
			if err == nil {
				if left != nil {
					left.SetWriteDeadline(time.Now().Add(writeWait))
					if err := left.WriteMessage(websocket.TextMessage, payload); err != nil {
						// Erro de escrita é tratado fechando e forçando a queda no loop de leitura
						left.Close()
					}
				}
				if right != nil {
					right.SetWriteDeadline(time.Now().Add(writeWait))
					if err := right.WriteMessage(websocket.TextMessage, payload); err != nil {
						right.Close()
					}
				}
			}
		}
	}
}

// sendPings envia de forma segura um pacote de Ping a ambos os lados ativos para validar se continuam vivos
func (m *Manager) sendPings() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.PlayerLeft != nil {
		m.PlayerLeft.SetWriteDeadline(time.Now().Add(writeWait))
		if err := m.PlayerLeft.WriteMessage(websocket.PingMessage, nil); err != nil {
			fmt.Printf("Ping falhou para Player Left, expurgando conexao zumbi: %v\n", err)
			m.PlayerLeft.Close()
			m.PlayerLeft = nil
			m.Engine.PlayerDisconnected()
		}
	}

	if m.PlayerRight != nil {
		m.PlayerRight.SetWriteDeadline(time.Now().Add(writeWait))
		if err := m.PlayerRight.WriteMessage(websocket.PingMessage, nil); err != nil {
			fmt.Printf("Ping falhou para Player Right, expurgando conexao zumbi: %v\n", err)
			m.PlayerRight.Close()
			m.PlayerRight = nil
			m.Engine.PlayerDisconnected()
		}
	}
}
