.PHONY: specs world generate-world calibrate run-daily-agent run-http-agent serve sync install stop start restart logs

SEED ?=
DB ?= data/catalog.db
WORLD_CONFIG ?= config/world-generation.v1.yaml
DEPLOY_HOST ?= uptick
LOG_LINES ?= 200
SIMULATOR_URL ?= http://81.176.229.58:8080

specs:
	go test -count=1 ./spec/...

world: generate-world

generate-world:
	@if [ -z "$(SEED)" ]; then echo "usage: make world SEED=1 [DB=data/catalog.db] [WORLD_CONFIG=config/world-generation.v1.yaml]"; exit 2; fi
	go run ./cmd/worldgen -seed "$(SEED)" -db "$(DB)" -config "$(WORLD_CONFIG)"

calibrate:
	go run ./cmd/calibrate -world-config "$(WORLD_CONFIG)" -calibration-config "config/calibration.v1.yaml"

run-daily-agent:
	@if [ -z "$(SEED)" ]; then echo "usage: make run-daily-agent SEED=1"; exit 2; fi
	go run ./cmd/dailyagent -seed "$(SEED)" -config "$(WORLD_CONFIG)" -data data

run-http-agent:
	go run ./cmd/httpagent -url "$(SIMULATOR_URL)" -seed "$(if $(strip $(SEED)),$(SEED),1)"

serve:
	go run ./cmd/server -addr "$${ADDR:-:8080}" -data "$${DATA:-data}" -config "$(WORLD_CONFIG)"

sync:
	DEPLOY_HOST="$(DEPLOY_HOST)" ./deploy/sync.sh

install: sync
	DEPLOY_HOST="$(DEPLOY_HOST)" ./deploy/install.sh

stop:
	DEPLOY_HOST="$(DEPLOY_HOST)" ./deploy/control.sh stop

start:
	DEPLOY_HOST="$(DEPLOY_HOST)" ./deploy/control.sh start

restart:
	DEPLOY_HOST="$(DEPLOY_HOST)" ./deploy/control.sh restart

logs:
	DEPLOY_HOST="$(DEPLOY_HOST)" LOG_LINES="$(LOG_LINES)" ./deploy/control.sh logs
