.PHONY: specs world generate-world calibrate run-daily-agent

SEED ?=
DB ?= data/catalog.db
WORLD_CONFIG ?= config/world-generation.v1.yaml

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
