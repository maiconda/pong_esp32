package game

import (
	"encoding/json"
	"math/rand"
	"sync"
	"time"
)

// Constantes físicas do jogo baseadas no display de 128x64
const (
	Width        = 128.0
	Height       = 64.0
	PaddleWidth  = 3.0
	PaddleHeight = 16.0
	BallSize     = 4.0

	PaddleLeftX  = 2.0
	PaddleRightX = 123.0 // 128 - 2 (margem) - 3 (largura)

	PaddleSpeed = 3.0 // Pixels por tick (a 30 FPS)
	MaxScore    = 11

	PointScoreTicks = 60  // 2.0 segundos de congelamento pós-ponto (a 30 FPS)
	GameOverTicks   = 150 // 5.0 segundos de congelamento pós-vitória (a 30 FPS)
)

// Engine gerencia a física, colisões e a máquina de estados completa do jogo de forma thread-safe
type Engine struct {
	mu sync.Mutex

	// Posições e velocidades físicas da bola
	BallX  float64
	BallY  float64
	BallVX float64
	BallVY float64

	// Posições e direções das raquetes
	PaddleLeftY   float64
	PaddleRightY  float64
	PaddleLeftDir float64 // -1 = Sobe, 1 = Desce, 0 = Parado
	PaddleRightDir float64

	// Lógica de placar e controle de jogadores "Prontos" no Lobby
	ScoreLeft       int
	ScoreRight      int
	PlayerLeftReady bool
	PlayerRightReady bool

	// Estado Atual: "waiting_players", "waiting_ready", "playing", "point_scored", "gameover"
	Status string

	// Controles internos de tempo de estados (medido em ticks de 33ms)
	timerTicks  int
	lastScorer  string // "left" ou "right" (usado para direcionar o saque)
}

// NewEngine inicializa e retorna a estrutura da engine do jogo
func NewEngine() *Engine {
	rand.Seed(time.Now().UnixNano())
	e := &Engine{
		Status: "waiting_players",
	}
	e.ResetPositions()
	return e
}

// ResetPositions centraliza a bola e as raquetes de forma padrão
func (e *Engine) ResetPositions() {
	e.BallX = Width/2 - BallSize/2
	e.BallY = Height/2 - BallSize/2
	e.BallVX = 0.0
	e.BallVY = 0.0

	e.PaddleLeftY = Height/2 - PaddleHeight/2
	e.PaddleRightY = Height/2 - PaddleHeight/2
	e.PaddleLeftDir = 0
	e.PaddleRightDir = 0
}

// PlayerConnected atualiza o status de transição de conexão de novos jogadores
func (e *Engine) PlayerConnected(count int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if count < 2 {
		e.Status = "waiting_players"
		e.PlayerLeftReady = false
		e.PlayerRightReady = false
		e.ResetPositions()
	} else if count == 2 && e.Status == "waiting_players" {
		// Ambos conectados, entra no lobby para esperar que fiquem prontos
		e.Status = "waiting_ready"
		e.PlayerLeftReady = false
		e.PlayerRightReady = false
		e.ScoreLeft = 0
		e.ScoreRight = 0
		e.ResetPositions()
	}
}

// PlayerDisconnected reinicia a engine para aguardar jogadores se um desconectar
func (e *Engine) PlayerDisconnected() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.Status = "waiting_players"
	e.PlayerLeftReady = false
	e.PlayerRightReady = false
	e.ScoreLeft = 0
	e.ScoreRight = 0
	e.ResetPositions()
}

// ToggleReady muda o status do jogador de pronto/lobby
func (e *Engine) ToggleReady(side string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Só aceita alteração de "pronto" na fase de lobby/espera
	if e.Status != "waiting_ready" {
		return
	}

	if side == "left" {
		e.PlayerLeftReady = !e.PlayerLeftReady
	} else if side == "right" {
		e.PlayerRightReady = !e.PlayerRightReady
	}

	// Se ambos estiverem prontos, inicia a partida de fato!
	if e.PlayerLeftReady && e.PlayerRightReady {
		e.Status = "playing"
		e.triggerReset("left") // O primeiro saque do jogo vai em direção à esquerda (P1)
	}
}

// triggerReset prepara o delay do saque centralizando a bola
func (e *Engine) triggerReset(targetDirection string) {
	e.BallX = Width/2 - BallSize/2
	e.BallY = Height/2 - BallSize/2
	e.BallVX = 0
	e.BallVY = 0
	e.timerTicks = PointScoreTicks
	e.lastScorer = targetDirection
}

// launchBall lança a bola com a velocidade inicial no início da rodada
func (e *Engine) launchBall() {
	speedX := 2.0
	if e.lastScorer == "left" {
		e.BallVX = -speedX // Lança para o jogador da esquerda
	} else {
		e.BallVX = speedX // Lança para o jogador da direita
	}
	
	// Angulação vertical aleatória
	e.BallVY = (rand.Float64()*2.0 - 1.0) * 1.2
}

// SetPaddleDir atualiza a direção do movimento da raquete de um jogador
func (e *Engine) SetPaddleDir(side string, dir float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Só permite movimentar raquetes se o jogo estiver rodando ou no congelamento pós-ponto
	if e.Status != "playing" && e.Status != "point_scored" {
		return
	}

	if side == "left" {
		e.PaddleLeftDir = dir
	} else if side == "right" {
		e.PaddleRightDir = dir
	}
}

// Update avança a simulação física por 1 tick/frame (chamado a 30 FPS)
func (e *Engine) Update() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Estados estáticos não rodam atualizações físicas
	if e.Status == "waiting_players" || e.Status == "waiting_ready" {
		return
	}

	// 2. Lida com atrasos congelados (Pontos ou Fim de Jogo)
	if e.timerTicks > 0 {
		e.timerTicks--
		if e.timerTicks == 0 {
			if e.Status == "point_scored" {
				// Pausa do ponto acabou: muda para playing e lança a bola
				e.Status = "playing"
				e.launchBall()
			} else if e.Status == "gameover" {
				// Pausa de fim de jogo acabou: volta para a tela de lobby "pronto"
				e.Status = "waiting_ready"
				e.PlayerLeftReady = false
				e.PlayerRightReady = false
				e.ScoreLeft = 0
				e.ScoreRight = 0
				e.ResetPositions()
			}
		}
		// Durante congelamentos leves de ponto, ainda permitimos a movimentação de raquetes
		if e.Status == "point_scored" {
			e.updatePaddles()
		}
		return
	}

	// 3. Atualiza posição das raquetes
	e.updatePaddles()

	// 4. Move a bola
	e.BallX += e.BallVX
	e.BallY += e.BallVY

	// 5. Colisão da bola com as paredes horizontais (Teto e Chão)
	if e.BallY <= 0 {
		e.BallY = 0
		e.BallVY = -e.BallVY
	} else if e.BallY >= Height-BallSize {
		e.BallY = Height - BallSize
		e.BallVY = -e.BallVY
	}

	// 6. Colisão com as Raquetes
	// Raquete da Esquerda (Player 1)
	if e.BallVX < 0 && e.BallX <= PaddleLeftX+PaddleWidth && e.BallX >= PaddleLeftX {
		if e.BallY+BallSize >= e.PaddleLeftY && e.BallY <= e.PaddleLeftY+PaddleHeight {
			// Ajusta a bola para fora da raquete para evitar travamentos
			e.BallX = PaddleLeftX + PaddleWidth + 0.1

			// Cálculo dinâmico do ângulo com base no ponto de impacto
			relativeY := (e.BallY + BallSize/2.0) - (e.PaddleLeftY + PaddleHeight/2.0)
			normalizedRelativeIntersectY := relativeY / (PaddleHeight / 2.0)
			
			// Inverte direção horizontal e aumenta velocidade em 5%
			e.BallVX = -e.BallVX * 1.05
			e.BallVY = normalizedRelativeIntersectY * 2.0 * 1.05
		}
	}

	// Raquete da Direita (Player 2)
	if e.BallVX > 0 && e.BallX+BallSize >= PaddleRightX && e.BallX+BallSize <= PaddleRightX+PaddleWidth {
		if e.BallY+BallSize >= e.PaddleRightY && e.BallY <= e.PaddleRightY+PaddleHeight {
			e.BallX = PaddleRightX - BallSize - 0.1

			relativeY := (e.BallY + BallSize/2.0) - (e.PaddleRightY + PaddleHeight/2.0)
			normalizedRelativeIntersectY := relativeY / (PaddleHeight / 2.0)

			e.BallVX = -e.BallVX * 1.05
			e.BallVY = normalizedRelativeIntersectY * 2.0 * 1.05
		}
	}

	// 7. Verificação de Ponto (Bola ultrapassa os limites horizontais)
	if e.BallX < 0 {
		// Ponto do Jogador da Direita
		e.ScoreRight++
		e.checkRoundEnd("left") // Passa "left" porque quem sofreu o ponto foi a esquerda (direcionamento de saque)
	} else if e.BallX > Width {
		// Ponto do Jogador da Esquerda
		e.ScoreLeft++
		e.checkRoundEnd("right") // Passa "right" porque quem sofreu o ponto foi a direita (direcionamento de saque)
	}
}

// checkRoundEnd verifica se o ponto decretou fim da partida ou apenas fim do round
func (e *Engine) checkRoundEnd(targetDirection string) {
	// Regras da vitória clássicas: 
	// 1. Um jogador precisa marcar pelo menos 11 pontos.
	// 2. Deve haver uma diferença mínima de 2 pontos (ex: 11-9, 12-10).
	if e.ScoreLeft >= MaxScore && (e.ScoreLeft-e.ScoreRight) >= 2 {
		e.Status = "gameover"
		e.timerTicks = GameOverTicks
		e.BallVX = 0
		e.BallVY = 0
	} else if e.ScoreRight >= MaxScore && (e.ScoreRight-e.ScoreLeft) >= 2 {
		e.Status = "gameover"
		e.timerTicks = GameOverTicks
		e.BallVX = 0
		e.BallVY = 0
	} else {
		// Caso contrário, apenas congela para rodar o próximo saque
		e.Status = "point_scored"
		e.triggerReset(targetDirection)
	}
}

// updatePaddles move e limita a posição vertical das raquetes
func (e *Engine) updatePaddles() {
	e.PaddleLeftY += e.PaddleLeftDir * PaddleSpeed
	if e.PaddleLeftY < 0 {
		e.PaddleLeftY = 0
	} else if e.PaddleLeftY > Height-PaddleHeight {
		e.PaddleLeftY = Height - PaddleHeight
	}

	e.PaddleRightY += e.PaddleRightDir * PaddleSpeed
	if e.PaddleRightY < 0 {
		e.PaddleRightY = 0
	} else if e.PaddleRightY > Height-PaddleHeight {
		e.PaddleRightY = Height - PaddleHeight
	}
}

// GetStateJSON serializa o estado para um formato JSON extremamente compacto e de baixo tráfego
func (e *Engine) GetStateJSON() ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	state := struct {
		Type    string `json:"type"`
		State   string `json:"state"`
		Bx      int    `json:"bx"`
		By      int    `json:"by"`
		P1      int    `json:"p1"`
		P2      int    `json:"p2"`
		S1      int    `json:"s1"`
		S2      int    `json:"s2"`
		P1Ready bool   `json:"p1_ready"`
		P2Ready bool   `json:"p2_ready"`
	}{
		Type:    "state",
		State:   e.Status,
		Bx:      int(e.BallX),
		By:      int(e.BallY),
		P1:      int(e.PaddleLeftY),
		P2:      int(e.PaddleRightY),
		S1:      e.ScoreLeft,
		S2:      e.ScoreRight,
		P1Ready: e.PlayerLeftReady,
		P2Ready: e.PlayerRightReady,
	}

	return json.Marshal(state)
}
