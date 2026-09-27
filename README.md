# Secretária Pessoal de IA no WhatsApp (Go + PostgreSQL + OpenAI)

Este projeto implementa uma Secretária Pessoal de Inteligência Artificial rodando diretamente no WhatsApp. O sistema é modular, escrito em Go Moderno, utiliza PostgreSQL via Docker para persistência e integra a API da OpenAI com **Function Calling (Tools)** para executar ações sob demanda, além de agendamentos automáticos e lembretes.

---

## 🛠️ Stack Tecnológica
- **Backend**: Go (Golang) com arquitetura modular.
- **WhatsApp**: Biblioteca `whatsmeow` para conexão nativa do WhatsApp Web.
- **Banco de Dados**: PostgreSQL v15 rodando em container Docker.
- **Inteligência Artificial**: API da OpenAI (GPT-4o) utilizando Function Calling (Tools).
- **Interface**: Vue 3 + Vite + Tailwind CSS (TypeScript), servida pelo próprio backend Go.

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
└── frontend/                # Painel (Vite + Vue 3 + Tailwind)
    ├── src/app/             # Shell, login, rotas
    ├── src/features/        # secretary/ e finance/
    └── src/api/             # Cliente HTTP
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

### 3. Login do painel
O painel exige login. Defina a senha de administrador (mínimo de 12 caracteres) e, de preferência, um segredo fixo para as sessões:
```bash
export ADMIN_PASSWORD="uma-senha-longa-e-unica"
export SESSION_SECRET="$(openssl rand -hex 32)"
```
Sem `ADMIN_PASSWORD` o backend sobe normalmente (a Secretária continua respondendo no WhatsApp), mas o painel fica bloqueado.

### 4. Migrations
As tabelas originais (baseline `001`) são criadas no boot. Qualquer migration nova é aplicada **manualmente**:
```bash
cd backend
go run . migrate status
go run . migrate apply
```
Em produção: `docker exec secretary_backend ./secretary migrate apply`.

### 5. Executar o Backend em Go
```bash
cd backend
go run .
```
O servidor HTTP iniciará na porta `8000` e servirá o painel compilado de `frontend/dist`.

Para desenvolver sem tocar em nenhuma conta real do WhatsApp, use `WHATSAPP_FAKE=connected` (ou `qr`): o backend não abre conexão com o WhatsApp.

### 6. Painel (Vite)
```bash
cd frontend
npm ci
npm run dev      # http://localhost:5173, com proxy de /api para :8000
npm run build    # gera frontend/dist, servido pelo backend
npm test && npm run typecheck
```

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
- **Lista de Compras**: *"Adicione leite, ovos e sabão na minha lista de compras"*, *"O que eu tenho na lista de compras?"*, *"Remova sabão da lista"*, ou *"Limpe a lista de compras"*. (A IA impede itens duplicados e permite que você insira vários itens de uma só vez!).

### 2. Gestor Financeiro (grupo do casal)
Módulo opcional (`FINANCE_ENABLED=true` + `secretary migrate apply`): um grupo de WhatsApp com vocês dois e o número da Secretária vira a interface financeira ("gastei 42 no almoço", foto de comprovante, "quanto gastamos esse mês?"), e o painel ganha a área **Financeiro** com dashboard, lançamentos, categorias e orçamentos.

Documentação: [arquitetura](docs/finance/ARCHITECTURE.md) · [setup](docs/finance/SETUP.md) · [uso no WhatsApp](docs/finance/WHATSAPP.md) · [segurança](docs/finance/SECURITY.md).

### 3. Rotinas Automáticas
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
cat > .env <<'ENV'
OPENAI_API_KEY=sua-chave-aqui
ADMIN_PASSWORD=uma-senha-longa-e-unica
SESSION_SECRET=um-segredo-aleatorio-de-32-caracteres-ou-mais
ENV

# Suba todos os containers compilando a imagem do backend Go
docker compose up -d --build
```
Acesse **`https://secretaria.infinitytech.net.br`** no seu navegador para abrir o painel!

### 5. Atualizar a Aplicação (script de deploy)
Depois do primeiro `docker compose up`, as atualizações são feitas pelo script `deploy.sh`, que envia os commits locais para o GitHub, atualiza o clone do servidor, reconstrói a imagem do backend (o frontend estático vai embutido nela) e reinicia o container. O container do PostgreSQL não é reconstruído, então a agenda e a sessão do WhatsApp são preservadas.

```bash
./deploy.sh              # Faz push da branch atual e deploya
./deploy.sh --no-push    # Deploya o que já está no origin
./deploy.sh --status     # Só mostra o estado atual do servidor
./deploy.sh --logs       # Acompanha os logs do backend após o deploy
```

O destino pode ser sobrescrito pelas variáveis de ambiente `SECRETARY_SERVER`, `SECRETARY_REMOTE_DIR` e `SECRETARY_HEALTH_URL`.

---

## 🕐 Fuso Horário

Todo cálculo de horário da aplicação acontece em **America/Sao_Paulo**, independente da configuração do servidor, do container ou do banco. O pacote `backend/timeutil` concentra essa responsabilidade:

- `timeutil.Now()` substitui `time.Now()` em todo o código de agendamento.
- `timeutil.ParseLocal()` interpreta as datas geradas pelo modelo. Uma data sem offset é lida como hora de Brasília; uma data com offset explícito (inclusive `Z`) é convertida para Brasília.
- `timeutil.AsLocalWallClock()` corrige os valores lidos das colunas `TIMESTAMP` sem fuso, que o driver pgx devolve marcados como UTC.
- O pool de conexões executa `SET TIME ZONE 'America/Sao_Paulo'` em cada nova conexão, e o worker de lembretes compara contra um horário calculado em Go em vez do `NOW()` do SQL.

## 🔔 Comportamento das Notificações

- **Resumo matinal**: enviado em um horário aleatório entre **07:00 e 08:00**, e **somente quando existe compromisso no dia**. Não há mensagem de "nenhum compromisso hoje".
- **Lembretes de compromisso**: disparam com um atraso aleatório de até 90 segundos.

O horário aleatório e o jitter são intencionais: uma conta que envia mensagem no mesmo segundo todos os dias exibe um padrão automatizado, e esse é um dos comportamentos que levam ao banimento do número no WhatsApp.

### Gerar um novo QR Code

Um QR Code de pareamento do WhatsApp vale poucos segundos, e o lote de códigos que o WhatsApp entrega de uma vez acaba. Quando isso acontece, a imagem na tela fica velha e o celular simplesmente não reconhece a leitura.

O botão **Gerar novo QR Code** no painel resolve isso: ele chama `POST /api/qrcode/refresh`, que descarta a tentativa de pareamento atual e pede um lote novo de códigos. O endpoint responde `409` quando o aparelho já está vinculado, porque nesse caso não há o que parear — é preciso desvincular o aparelho no celular primeiro.
