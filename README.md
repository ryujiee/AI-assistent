# Secretária Pessoal de IA no WhatsApp (Go + PostgreSQL + OpenAI)

Este projeto implementa uma Secretária Pessoal de Inteligência Artificial rodando diretamente no WhatsApp. O sistema é modular, escrito em Go Moderno, utiliza PostgreSQL via Docker para persistência e integra a API da OpenAI com **Function Calling (Tools)** para executar ações sob demanda, além de agendamentos automáticos e lembretes.

---

## 🛠️ Stack Tecnológica
- **Backend**: Go (Golang) com arquitetura modular.
- **WhatsApp**: Biblioteca `whatsmeow` para conexão nativa do WhatsApp Web.
- **Banco de Dados**: PostgreSQL v15 rodando em container Docker.
- **Inteligência Artificial**: API da OpenAI (GPT-4o) utilizando Function Calling (Tools).
- **Interface**: Vue.js 3 minimalista com Tailwind CSS.

---

## 📂 Estrutura do Projeto
```
AI-assistent/
├── docker-compose.yml       # Orquestração do Postgres e pgAdmin
├── README.md                # Documentação do projeto
├── backend/                 # Código do Backend em Go
│   ├── main.go              # Ponto de entrada
│   ├── go.mod               # Módulo do Go
│   ├── config/              # Configurações do sistema
│   ├── db/                  # Pool pgxpool e migrações
│   ├── engine/              # Cron, timers dinâmicos e alertas
│   ├── openai/              # Conexão OpenAI e Function Calling
│   ├── whatsapp/            # Conector whatsmeow e listeners
│   └── web/                 # Servidor HTTP de status e configuração
└── frontend/                # Interface administrativa
    └── index.html           # SPA com Vue.js 3 e Tailwind CSS
```

---

## 🚀 Como Executar Localmente

### 1. Iniciar Banco de Dados (Docker)
No diretório raiz do projeto, execute o comando para iniciar o PostgreSQL e o pgAdmin em segundo plano:
```bash
docker compose up -d
```
- **PostgreSQL**: Rodando localmente na porta `5432` com usuário `secretary_user` e senha `secretary_password`.
- **pgAdmin**: Disponível em `http://localhost:8080` (Login: `admin@admin.com` / Senha: `admin`).

---

### 2. Configurar Variáveis de Ambiente
Você deve configurar sua chave de API da OpenAI antes de rodar o backend. No seu terminal Linux (ou arquivo `.bashrc`/`.zshrc`), configure:

```bash
export OPENAI_API_KEY="sua-chave-api-da-openai"
```

Opcional (se você desejar mudar o banco ou a porta padrão do servidor):
```bash
export DATABASE_URL="postgres://secretary_user:secretary_password@localhost:5432/secretary_db?sslmode=disable"
export PORT="8000"
```

---

### 3. Executar o Backend em Go
Navegue até o diretório `backend` e execute o servidor:
```bash
cd backend
go run main.go
```
O servidor HTTP iniciará na porta `8000`.

---

### 4. Abrir a Interface Web (Dashboard)
Você pode abrir o arquivo `frontend/index.html` diretamente em qualquer navegador Web (dê dois cliques no arquivo ou utilize a extensão Live Server).
Como o backend suporta CORS, o painel se comunicará automaticamente com a API na porta `8000`.

No Painel de Controle:
1. Insira o seu número do WhatsApp (ex: `5511999999999`) no campo de **Número Alvo** e clique em **Salvar**. Isso garante que a IA só responda a você.
2. Escaneie o **QR Code** exibido na tela usando seu celular no aplicativo do WhatsApp (Configurações -> Aparelhos Conectados -> Conectar Aparelho).
3. Uma vez conectado, o status mudará para verde **Conectado** no painel de controle.

---

## 🤖 Como Funciona a Secretária IA

### 1. Interação via WhatsApp
Envie mensagens para o número do WhatsApp configurado para o bot:
- **Salvar Lembretes/Notas**: *"Guarde que a senha do Wi-Fi da empresa é 1234"* ou *"Anote que preciso enviar o relatório comercial amanhã"*.
- **Agendar Eventos**: *"Agende uma reunião com o cliente João amanhã às 14:00 chamada Alinhamento Mensal"*.
- **Pesquisar Anotações**: *"O que eu tenho anotado sobre Wi-Fi?"* ou *"Qual a senha do Wi-Fi que salvei?"*.
- **Iniciar Timers**: *"Coloque um timer de 5 minutos para eu tirar o bolo do forno"*.

### 2. Rotinas Automáticas
- **Resumo Matinal (Cron)**: Todos os dias às **07:30 da manhã**, a aplicação buscará seus compromissos agendados no banco de dados, enviará para a OpenAI criar um bom dia personalizado e amigável e enviará para seu WhatsApp.
- **Alertas Antecipados**: Um worker rodando a cada minuto verifica se existem compromissos próximos no banco e envia uma mensagem de aviso no seu WhatsApp **15 minutos antes** do início.
- **Timers Dinâmicos**: Goroutines dedicadas gerenciam o tempo em memória e disparam alertas imediatos assim que os minutos de um timer se encerram.

---

## 🌐 Implantação em Produção (servidor)

Para rodar em um servidor de produção com o domínio **`secretaria.infinitytech.net.br`**, siga o passo a passo abaixo.

### 1. Requisitos no Servidor
Garanta que o servidor (ex: Ubuntu Linux) possui instalados:
- **Docker** e **Docker Compose**
- **Nginx** (para proxy reverso e SSL)

### 2. Configurar o Nginx como Proxy Reverso
Crie um arquivo de configuração para o site no Nginx:
```bash
sudo nano /etc/nginx/sites-available/secretaria.infinitytech.net.br
```

Insira a seguinte configuração, redirecionando o tráfego HTTP para a porta `8000` (onde o container Go está rodando):
```nginx
server {
    server_name secretaria.infinitytech.net.br;

    location / {
        proxy_pass http://localhost:8000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Ative o site e reinicie o Nginx:
```bash
sudo ln -s /etc/nginx/sites-available/secretaria.infinitytech.net.br /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl restart nginx
```

### 3. Configurar SSL Seguro (HTTPS) com Let's Encrypt
Rode o Certbot para gerar os certificados SSL e configurar o redirecionamento automático para HTTPS:
```bash
sudo apt update
sudo apt install certbot python3-certbot-nginx -y
sudo certbot --nginx -d secretaria.infinitytech.net.br
```
Siga as instruções na tela para finalizar.

### 4. Clonar e Iniciar a Aplicação via Docker
No servidor, clone o repositório e configure as credenciais:
```bash
git clone git@github.com:ryujiee/AI-assistent.git
cd AI-assistent

# Crie o arquivo de configuração de ambiente (.env)
echo "OPENAI_API_KEY=sua-chave-aqui" > .env

# Suba todos os containers compilando a imagem do backend Go
docker compose up -d --build
```
Acesse **`https://secretaria.infinitytech.net.br`** no seu navegador para abrir o painel!
