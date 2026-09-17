#!/usr/bin/env bash
#
# Deploys the AI Personal Secretary (backend + frontend) to the production server.
#
# The server keeps a git clone of this repository, so a deploy is: push the
# local commits to origin, pull them on the server, rebuild the backend image
# and restart the stack. The PostgreSQL container is never rebuilt, which keeps
# the appointment history and the WhatsApp session intact.
#
# Usage:
#   ./deploy.sh                 Push the current branch and deploy it
#   ./deploy.sh --no-push       Deploy what is already on origin
#   ./deploy.sh --frontend      Deploy only the frontend (no image rebuild)
#   ./deploy.sh --logs          Follow the backend logs after deploying
#   ./deploy.sh --status        Only show the current state of the server
#
set -euo pipefail

SERVER="${SECRETARY_SERVER:-root@chat.infinitytech.net.br}"
REMOTE_DIR="${SECRETARY_REMOTE_DIR:-/opt/AI-assistent}"
HEALTH_URL="${SECRETARY_HEALTH_URL:-https://secretaria.infinitytech.net.br/api/status}"

PUSH=true
FRONTEND_ONLY=false
FOLLOW_LOGS=false
STATUS_ONLY=false

for arg in "$@"; do
    case "$arg" in
        --no-push)  PUSH=false ;;
        --frontend) FRONTEND_ONLY=true ;;
        --logs)     FOLLOW_LOGS=true ;;
        --status)   STATUS_ONLY=true ;;
        -h|--help)  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "Unknown option: $arg (try --help)" >&2; exit 1 ;;
    esac
done

BOLD=$'\033[1m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; RESET=$'\033[0m'
step() { printf '\n%s==> %s%s\n' "$BOLD" "$1" "$RESET"; }
ok()   { printf '%s  ok%s %s\n' "$GREEN" "$RESET" "$1"; }
warn() { printf '%s  !!%s %s\n' "$YELLOW" "$RESET" "$1"; }
die()  { printf '\n%serro:%s %s\n' "$RED" "$RESET" "$1" >&2; exit 1; }

remote()     { ssh -o ConnectTimeout=15 "$SERVER" "$@"; }
remote_tty() { ssh -t -o ConnectTimeout=15 "$SERVER" "$@"; }

show_status() {
    step "Estado atual do servidor"
    remote "cd '$REMOTE_DIR' && git log --oneline -1 && docker compose ps"
    printf '\n'
    if curl -fsS --max-time 10 "$HEALTH_URL" 2>/dev/null; then
        printf '\n'
        ok "API respondendo em $HEALTH_URL"
    else
        warn "API não respondeu em $HEALTH_URL"
    fi
}

# ---------------------------------------------------------------------------

step "Verificando acesso a $SERVER"
remote "test -d '$REMOTE_DIR'" || die "não consegui acessar $REMOTE_DIR em $SERVER via SSH."
ok "SSH e $REMOTE_DIR acessíveis"

if [ "$STATUS_ONLY" = true ]; then
    show_status
    exit 0
fi

cd "$(dirname "$0")"
BRANCH=$(git rev-parse --abbrev-ref HEAD)

if [ "$PUSH" = true ]; then
    step "Enviando código local para o origin (branch $BRANCH)"
    if [ -n "$(git status --porcelain)" ]; then
        git status --short
        die "há alterações não commitadas. Faça o commit (ou use --no-push) antes de deployar."
    fi
    git push origin "$BRANCH"
    ok "branch $BRANCH enviada"
fi

step "Atualizando o código no servidor"
remote "set -e; cd '$REMOTE_DIR'; git fetch origin '$BRANCH'; git checkout '$BRANCH'; git reset --hard 'origin/$BRANCH'; git log --oneline -1"
ok "servidor na última versão de $BRANCH"

if [ "$FRONTEND_ONLY" = true ]; then
    # The frontend is a static file baked into the backend image, so even a
    # frontend-only change needs the image rebuilt - but not the dependencies.
    step "Reconstruindo apenas a imagem do backend (frontend estático embutido)"
else
    step "Reconstruindo e reiniciando o backend"
fi

remote "set -e; cd '$REMOTE_DIR'; docker compose up -d --build backend"
ok "container do backend reiniciado"

step "Conferindo o horário dentro do container"
remote "docker exec secretary_backend date" || warn "não consegui ler a data do container"
remote "docker exec secretary_postgres psql -U secretary_user -d secretary_db -tAc \"SELECT now();\"" \
    || warn "não consegui consultar o banco"

step "Aguardando o backend subir"
for _ in $(seq 1 15); do
    if remote "curl -fsS --max-time 5 http://localhost:8000/api/status" >/dev/null 2>&1; then
        ok "backend respondendo na porta 8000"
        break
    fi
    sleep 2
done

show_status

step "Últimas linhas do log do backend"
remote "cd '$REMOTE_DIR' && docker compose logs --tail 40 backend"

if [ "$FOLLOW_LOGS" = true ]; then
    step "Seguindo os logs (Ctrl+C para sair)"
    remote_tty "cd '$REMOTE_DIR' && docker compose logs -f --tail 20 backend"
fi

printf '\n%sDeploy concluído.%s Painel: https://secretaria.infinitytech.net.br\n' "$GREEN" "$RESET"
