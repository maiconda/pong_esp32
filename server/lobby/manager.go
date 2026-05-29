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
		return true // Permite qualquer origem para facilidade de testes online
	},
}

// Manager coordena as conexões de rede ativas e integra com a máquina de estados do jogo
type Manager struct {
	mu          sync.Mutex
	PlayerLeft  *websocket.Conn
	PlayerRight *websocket.Conn
	Engine      *game.Engine
	tickCount   int // Ticks acumulados a 30 FPS para controle de ping
}

// NewManager cria e configura um novo gerenciador de lobby
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

// HandleWS processa o handshake inicial de novas conexões
func (m *Manager) HandleWS(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[WS CONNECT] Nova conexao HTTP recebida de %s. Iniciando Handshake...\n", r.RemoteAddr)
	
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[WS CONNECT] Falha ao fazer o upgrade da conexao de %s: %v\n", r.RemoteAddr, err)
		return
	}

	// Define políticas de segurança e monitoramento de queda
	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	
	// Sempre que receber um Pong, renova a data de tolerância
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	m.mu.Lock()
	var assignedSide string

	// Tenta alocar vaga livre
	if m.PlayerLeft == nil {
		m.PlayerLeft = conn
		assignedSide = "left"
		fmt.Printf("[LOBBY ASSIGN] Cliente %s registrado com sucesso na vaga [ESQUERDA].\n", r.RemoteAddr)
	} else if m.PlayerRight == nil {
		m.PlayerRight = conn
		assignedSide = "right"
		fmt.Printf("[LOBBY ASSIGN] Cliente %s registrado com sucesso na vaga [DIREITA].\n", r.RemoteAddr)
	} else {
		m.mu.Unlock()
		fmt.Printf("[LOBBY REJECT] Conexao recusada para %s: Ambas as vagas ja estao ocupadas.\n", r.RemoteAddr)
		errPayload, _ := json.Marshal(map[string]string{
			"type":  "error",
			"error": "lobby_full",
		})
		conn.WriteMessage(websocket.TextMessage, errPayload)
		conn.Close()
		return
	}

	// Calcula total de jogadores ativos e notifica a engine
	count := 0
	if m.PlayerLeft != nil {
		count++
	}
	if m.PlayerRight != nil {
		count++
	}
	m.Engine.PlayerConnected(count)
	m.mu.Unlock()

	// Envia mensagem de boas-vindas com o lado do jogador
	setupMsg := SetupMessage{
		Type: "setup",
		Side: assignedSide,
	}
	if payload, err := json.Marshal(setupMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, payload)
	}

	// Inicializa a escuta assíncrona do cliente
	go m.readLoop(conn, assignedSide)
}

// readLoop mantém a escuta de comandos ativos do jogador
func (m *Manager) readLoop(conn *websocket.Conn, side string) {
	// A desconexão é centralizada no defer por segurança
	defer m.disconnect(conn, side, "fim do loop de rede/leitura")

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			// Erro na leitura indica desconexão legítima ou estouro de deadline
			break
		}

		// A cada mensagem lida com sucesso, renovamos a tolerância de conexão
		conn.SetReadDeadline(time.Now().Add(pongWait))

		// Processa mensagens recebidas
		var msg ClientMessage
		if err := json.Unmarshal(payload, &msg); err == nil {
			if msg.Type == "input" {
				// Repassa movimento de raquete para a Engine
				m.Engine.SetPaddleDir(side, msg.Dir)
			} else if msg.Type == "ready" {
				// Repassa clique de lobby para a Engine
				m.Engine.ToggleReady(side)
			}
		} else {
			fmt.Printf("[WS RECV ERROR] Falha ao desserializar JSON de [%s]: %s\n", side, string(payload))
		}
	}
}

// disconnect é a função unificada e thread-safe que limpa as vagas de jogadores
func (m *Manager) disconnect(conn *websocket.Conn, side string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Garante que só limpará a vaga se a conexão sendo limpa ainda for a ativa daquele lado
	if side == "left" && m.PlayerLeft == conn {
		m.PlayerLeft = nil
		conn.Close()
		fmt.Printf("[WS DISCONNECT] Vaga [ESQUERDA] liberada. Motivo: %s\n", reason)
		m.Engine.PlayerDisconnected()
	} else if side == "right" && m.PlayerRight == conn {
		m.PlayerRight = nil
		conn.Close()
		fmt.Printf("[WS DISCONNECT] Vaga [DIREITA] liberada. Motivo: %s\n", reason)
		m.Engine.PlayerDisconnected()
	}
}

// StartGameLoop roda a 30 FPS computando as regras e atualizando ambos os clientes em tempo real
func (m *Manager) StartGameLoop() {
	ticker := time.NewTicker(33 * time.Millisecond) // ~30 FPS
	defer ticker.Stop()

	for range ticker.C {
		// 1. Atualiza regras físicas e estados na engine
		m.Engine.Update()

		// 2. Heartbeat periódico: Envia pings a cada 4 segundos (~120 ticks) para expurgar zumbis
		m.tickCount++
		if m.tickCount >= 120 {
			m.tickCount = 0
			m.sendPings()
		}

		m.mu.Lock()
		left := m.PlayerLeft
		right := m.PlayerRight
		m.mu.Unlock()

		// 3. Transmite o novo JSON de estado para os clientes ativos
		if left != nil || right != nil {
			payload, err := m.Engine.GetStateJSON()
			if err == nil {
				if left != nil {
					left.SetWriteDeadline(time.Now().Add(writeWait))
					if err := left.WriteMessage(websocket.TextMessage, payload); err != nil {
						// Queda de escrita aciona o expurgo assíncrono
						fmt.Printf("[WS WRITE ERROR] Falha ao transmitir estado para [ESQUERDA]: %v\n", err)
						go m.disconnect(left, "left", "erro na transmissao de dados (Write)")
					}
				}
				if right != nil {
					right.SetWriteDeadline(time.Now().Add(writeWait))
					if err := right.WriteMessage(websocket.TextMessage, payload); err != nil {
						fmt.Printf("[WS WRITE ERROR] Falha ao transmitir estado para [DIREITA]: %v\n", err)
						go m.disconnect(right, "right", "erro na transmissao de dados (Write)")
					}
				}
			}
		}
	}
}

// sendPings envia pacotes de Ping a ambos os lados ativos para expurgar zumbis imediatamente
func (m *Manager) sendPings() {
	m.mu.Lock()
	left := m.PlayerLeft
	right := m.PlayerRight
	m.mu.Unlock()

	if left != nil {
		left.SetWriteDeadline(time.Now().Add(writeWait))
		if err := left.WriteMessage(websocket.PingMessage, nil); err != nil {
			fmt.Printf("[WS PING ERROR] Falha ao enviar Ping para [ESQUERDA]. Expurgando conexao zumbi.\n")
			go m.disconnect(left, "left", "falha no envio de Ping (Heartbeat)")
		}
	}

	if right != nil {
		right.SetWriteDeadline(time.Now().Add(writeWait))
		if err := right.WriteMessage(websocket.PingMessage, nil); err != nil {
			fmt.Printf("[WS PING ERROR] Falha ao enviar Ping para [DIREITA]. Expurgando conexao zumbi.\n")
			go m.disconnect(right, "right", "falha no envio de Ping (Heartbeat)")
		}
	}
}
