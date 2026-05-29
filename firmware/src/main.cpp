#include <WiFi.h>
#include <Wire.h>
#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>
#include <WebSocketsClient.h>
#include <ArduinoJson.h> // Para desserializar o estado e serializar os inputs

// 1. Configurações da tela OLED SSD1306 via I2C
#define SCREEN_WIDTH 128
#define SCREEN_HEIGHT 64
#define OLED_RESET    -1
#define SCREEN_ADDRESS 0x3C
Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, OLED_RESET);

// 2. Definição dos pinos físicos dos botões (Pull-up interno)
#define PIN_BTN_UP   12
#define PIN_BTN_DOWN 14

// 3. Configurações de Conectividade (Padrão para Wokwi e se.maiconda.com)
const char* wifiSSID = "Wokwi-GUEST";
const char* wifiPass = "";
const char* wsHost   = "se.maiconda.com";
const uint16_t wsPort = 443;
const char* wsPath   = "/ws";

// Instâncias Globais
WebSocketsClient webSocket;
String playerSide = "";      // "left" ou "right" (atribuído pelo servidor)
String gameState = "waiting_players"; // Estado atual do jogo

// Variáveis para otimização de envio de mensagens e debouncing dos botões
int lastSentDir = 0;         // Guarda a última direção enviada (-1, 1, 0)
bool lastUpState = HIGH;     // Estado anterior do botão Cima (HIGH = solto)
bool lastDownState = HIGH;   // Estado anterior do botão Baixo

// Função para exibir textos formatados simples no visor OLED
void drawText(const char* title, const char* line1 = "", const char* line2 = "", const char* line3 = "") {
  display.clearDisplay();
  display.setTextColor(SSD1306_WHITE);
  
  // Desenha Título
  display.setTextSize(1);
  display.setCursor(0, 0);
  display.println(title);
  display.println("---------------------");

  // Linhas de Conteúdo
  display.setCursor(0, 20);
  display.println(line1);
  display.setCursor(0, 35);
  display.println(line2);
  display.setCursor(0, 50);
  display.println(line3);
  
  display.display();
}

// Renderiza a partida de Ping Pong em tempo real na resolução de 128x64 (Sem Placar)
void drawActiveGame(int bx, int by, int p1, int p2) {
  display.clearDisplay();
  
  // 1. Linha central tracejada
  for (int y = 0; y < SCREEN_HEIGHT; y += 4) {
    display.drawFastVLine(64, y, 2, SSD1306_WHITE);
  }

  // 2. Raquete Esquerda (Player 1) - largura 3, altura 16
  display.fillRect(2, p1, 3, 16, SSD1306_WHITE);

  // 3. Raquete Direita (Player 2) - largura 3, altura 16
  display.fillRect(123, p2, 3, 16, SSD1306_WHITE);

  // 4. Bola (Quadrado de 4x4)
  display.fillRect(bx, by, 4, 4, SSD1306_WHITE);

  display.display();
}

// Desenha a tela de congelamento pós-ponto (Mostrando Placar Gigante)
void drawPointScored(int s1, int s2) {
  display.clearDisplay();
  display.setTextColor(SSD1306_WHITE);

  // Placar Gigante centralizado
  display.setTextSize(2);
  display.setCursor(35, 10);
  display.printf("%d - %d", s1, s2);

  // Mensagem auxiliar
  display.setTextSize(1);
  display.setCursor(15, 45);
  display.println("PONTO MARCADO!");
  
  display.display();
}

// Desenha a tela de Game Over e Vencedor
void drawGameOver(int s1, int s2) {
  display.clearDisplay();
  display.setTextColor(SSD1306_WHITE);

  // Título e Placar Final
  display.setTextSize(1);
  display.setCursor(30, 0);
  display.println("FIM DE JOGO");
  display.setCursor(45, 15);
  display.printf("[%d - %d]", s1, s2);

  // Determina e exibe o vencedor
  display.setTextSize(1);
  display.setCursor(10, 40);
  if (s1 >= 11 && (s1 - s2) >= 2) {
    display.println("VENCEDOR: ESQUERDA");
  } else {
    display.println("VENCEDOR: DIREITA");
  }
  
  display.display();
}

// Envia comando JSON para o WebSocket do servidor Go
void sendWSMessage(String jsonPayload) {
  webSocket.sendTXT(jsonPayload);
}

// Callback de processamento de eventos do WebSocket
void webSocketEvent(WStype_t type, uint8_t * payload, size_t length) {
  switch(type) {
    case WStype_DISCONNECTED:
      Serial.println("[WS] Status: Desconectado.");
      gameState = "waiting_players";
      drawText("PONG MULTIPLAYER", "Status: Offline", "Tentando conectar...", "IP: 10.10.0.2");
      break;

    case WStype_CONNECTED:
      Serial.println("[WS] Status: Conectado!");
      drawText("PONG MULTIPLAYER", "Status: Conectado", "Aguardando setup...");
      break;

    case WStype_TEXT: {
      // Faz o parsing do JSON recebido do servidor Go
      JsonDocument doc;
      DeserializationError error = deserializeJson(doc, payload);
      if (error) {
        Serial.print("Erro no JSON: ");
        Serial.println(error.c_str());
        return;
      }

      String msgType = doc["type"] | "";

      // 1. Mensagem de Configuração Inicial (Lado do jogador)
      if (msgType == "setup") {
        playerSide = doc["side"] | "";
        Serial.printf("[WS] Setup recebido! Lado: %s\n", playerSide.c_str());
        drawText("PONG MULTIPLAYER", "Lado Atribuido:", playerSide.equalsIgnoreCase("left") ? "ESQUERDA" : "DIREITA");
      } 
      // 2. Mensagem de Frame e Estado do Jogo
      else if (msgType == "state") {
        gameState = doc["state"] | "waiting_players";
        
        int bx = doc["bx"] | 0;
        int by = doc["by"] | 0;
        int p1 = doc["p1"] | 0;
        int p2 = doc["p2"] | 0;
        int s1 = doc["s1"] | 0;
        int s2 = doc["s2"] | 0;
        bool p1Ready = doc["p1_ready"] | false;
        bool p2Ready = doc["p2_ready"] | false;

        // Renderiza na tela do OLED SSD1306 baseado na Máquina de Estados recebida do Go
        if (gameState == "waiting_players") {
          drawText("PONG MULTIPLAYER", "Lado Atribuido:", playerSide.equalsIgnoreCase("left") ? "ESQUERDA" : "DIREITA", "AGUARDANDO P2...");
        } 
        else if (gameState == "waiting_ready") {
          // Renderiza tela de Lobby com Prontidão dos dois lados
          char line1[30];
          char line2[30];
          sprintf(line1, "VOCE (%s): %s", playerSide.equalsIgnoreCase("left") ? "ESQ" : "DIR", 
                  (playerSide.equalsIgnoreCase("left") ? p1Ready : p2Ready) ? "PRONTO" : "ESPERANDO");
          sprintf(line2, "RIVAL: %s", 
                  (playerSide.equalsIgnoreCase("left") ? p2Ready : p1Ready) ? "PRONTO" : "ESPERANDO");
          
          drawText("LOBBY: APERTE BOTAO", line1, line2, "Clique p/ Ready!");
        } 
        else if (gameState == "playing") {
          // Gameplay ativo: OCULTA O PLACAR (Tela limpa)
          drawActiveGame(bx, by, p1, p2);
        } 
        else if (gameState == "point_scored") {
          // Ponto marcado: Mostra o placar gigante na tela
          drawPointScored(s1, s2);
        } 
        else if (gameState == "gameover") {
          // Fim de jogo: Mostra vencedor
          drawGameOver(s1, s2);
        }
      }
      break;
    }
    
    case WStype_ERROR:
      Serial.println("[WS] Erro ocorrido na conexao.");
      break;
  }
}

void setup() {
  Serial.begin(115200);
  delay(100);

  // Inicializa botões físicos com pull-up interno
  pinMode(PIN_BTN_UP, INPUT_PULLUP);
  pinMode(PIN_BTN_DOWN, INPUT_PULLUP);

  // Inicializa a tela OLED
  if(!display.begin(SSD1306_SWITCHCAPVCC, SCREEN_ADDRESS)) {
    Serial.println(F("ERRO: Display nao encontrado!"));
    for(;;);
  }
  
  drawText("PONG MULTIPLAYER", "Iniciando...", "Buscando Wi-Fi...");

  // Conecta ao Wi-Fi virtual do Wokwi
  WiFi.begin(wifiSSID, wifiPass);
  Serial.print("Conectando ao Wi-Fi...");
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }
  Serial.println("\nWi-Fi Conectado!");
  drawText("PONG MULTIPLAYER", "Wi-Fi: OK", "Conectando WS...");

  // Conecta ao WebSocket Seguro do servidor Go
  webSocket.beginSSL(wsHost, wsPort, wsPath);
  webSocket.onEvent(webSocketEvent);
  webSocket.setReconnectInterval(5000); // Reconecta em 5s se cair
}

void loop() {
  // Mantém a escuta do WebSocket
  webSocket.loop();

  // Lê os botões físicos
  bool upPressed = (digitalRead(PIN_BTN_UP) == LOW);
  bool downPressed = (digitalRead(PIN_BTN_DOWN) == LOW);

  // 1. SE ESTIVER NO LOBBY (Lógica de Pronto)
  // Ao clicar em QUALQUER botão (Cima ou Baixo), envia {"type": "ready"}
  // Usamos detecção de borda de descida (pressionar o botão) para evitar envios infinitos
  if (gameState == "waiting_ready") {
    bool upJustPressed = (upPressed && lastUpState == HIGH);
    bool downJustPressed = (downPressed && lastDownState == HIGH);

    if (upJustPressed || downJustPressed) {
      Serial.println("[BOTAO] Pressionado no Lobby! Alternando status de Pronto.");
      sendWSMessage("{\"type\":\"ready\"}");
      delay(150); // debounce simples
    }
  }

  // 2. SE ESTIVER JOGANDO (Lógica de Movimento da Raquete)
  // Envia a direção apenas quando o estado mudar para otimizar largura de banda
  else if (gameState == "playing") {
    int targetDir = 0;
    if (upPressed) {
      targetDir = -1; // Mover para Cima (decrementa Y)
    } else if (downPressed) {
      targetDir = 1;  // Mover para Baixo (incrementa Y)
    }

    // Só envia pacote se a direção mudar
    if (targetDir != lastSentDir) {
      lastSentDir = targetDir;
      
      // Constrói o JSON dinamicamente
      char payload[60];
      sprintf(payload, "{\"type\":\"input\",\"dir\":%d}", lastSentDir);
      
      Serial.printf("[INPUT] Enviando direcao: %d\n", lastSentDir);
      sendWSMessage(payload);
    }
  }

  // Guarda o estado anterior dos botões para a detecção de borda no próximo loop
  lastUpState = digitalRead(PIN_BTN_UP) ? HIGH : LOW;
  lastDownState = digitalRead(PIN_BTN_DOWN) ? HIGH : LOW;
  
  delay(10); // Intervalo confortável de ciclo de polling
}
