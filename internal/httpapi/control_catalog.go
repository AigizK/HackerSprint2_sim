package httpapi

func controlCommandCatalog() []commandDefinitionResponse {
	empty := closedSchema(map[string]any{}, nil)
	id := resourceIDCatalogSchema()
	name := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	serverID := closedSchema(map[string]any{"server_id": id}, []string{"server_id"})
	databaseID := closedSchema(map[string]any{"database_id": id}, []string{"database_id"})
	result := func(properties map[string]any, required ...string) map[string]any {
		return closedSchema(properties, required)
	}
	rule := firewallRuleViewCatalogSchema()
	serverType := serverTypeCatalogSchema()
	server := serverCatalogSchema()
	database := databaseCatalogSchema()
	backup := backupCatalogSchema()
	site := siteCatalogSchema()
	definitions := []commandDefinitionResponse{
		{Command: "firewall.rules.list", Description: "Список firewall-правил в порядке применения", ParamsSchema: empty, ResultSchema: result(map[string]any{"rules": arrayCatalogSchema(rule)}, "rules")},
		{Command: "firewall.rules.upsert", Description: "Создать или полностью заменить firewall-правило", ParamsSchema: firewallRuleCatalogSchema(), ResultSchema: result(map[string]any{"rule": rule}, "rule")},
		{Command: "firewall.rules.delete", Description: "Удалить firewall-правило", ParamsSchema: closedSchema(map[string]any{"rule_id": id}, []string{"rule_id"}), ResultSchema: result(map[string]any{"rule_id": id, "deleted": map[string]any{"type": "boolean", "const": true}}, "rule_id", "deleted")},
		{Command: "server.types.list", Description: "Каталог доступных типов серверов", ParamsSchema: closedSchema(map[string]any{"role": enumSchema("backend", "database")}, nil), ResultSchema: result(map[string]any{"types": arrayCatalogSchema(serverType)}, "types")},
		{Command: "server.create", Description: "Создать типизированный сервер", ParamsSchema: closedSchema(map[string]any{"name": name, "role": enumSchema("backend", "database"), "instance_type": id}, []string{"name", "role", "instance_type"}), ResultSchema: result(map[string]any{"server": server}, "server"), Execution: "asynchronous"},
		{Command: "server.inspect", Description: "Получить состояние и ресурсы сервера", ParamsSchema: serverID, ResultSchema: result(map[string]any{"server": server}, "server")},
		{Command: "server.delete", Description: "Освободить сервер после drain", ParamsSchema: serverID, ResultSchema: result(map[string]any{"server_id": id, "deleted": map[string]any{"type": "boolean", "const": true}}, "server_id", "deleted"), Execution: "asynchronous"},
		{Command: "database.create", Description: "Создать пустую БД на сервере", ParamsSchema: closedSchema(map[string]any{"server_id": id, "name": name}, []string{"server_id", "name"}), ResultSchema: result(map[string]any{"database": database}, "database"), TargetAuthRequired: true},
		{Command: "database.inspect", Description: "Получить состояние БД и её нагрузку", ParamsSchema: databaseID, ResultSchema: result(map[string]any{"database": database}, "database"), TargetAuthRequired: true},
		{Command: "database.backup", Description: "Создать внешний бэкап данных БД", ParamsSchema: databaseID, ResultSchema: result(map[string]any{"backup": backup}, "backup"), TargetAuthRequired: true, Execution: "asynchronous"},
		{Command: "database.backups.list", Description: "Список внешних бэкапов", ParamsSchema: closedSchema(map[string]any{"database_id": id}, nil), ResultSchema: result(map[string]any{"backups": arrayCatalogSchema(backup)}, "backups")},
		{Command: "database.restore", Description: "Восстановить бэкап в пустую БД", ParamsSchema: closedSchema(map[string]any{"database_id": id, "backup_id": id}, []string{"database_id", "backup_id"}), ResultSchema: result(map[string]any{"database": database}, "database"), TargetAuthRequired: true, Execution: "asynchronous"},
		{Command: "site.config.get", Description: "Получить управляемую конфигурацию сайта", ParamsSchema: empty, ResultSchema: result(map[string]any{"site": site}, "site")},
		{Command: "site.stop", Description: "Остановить сайт и дождаться соединений", ParamsSchema: empty, ResultSchema: result(map[string]any{"site": site}, "site"), Execution: "asynchronous"},
		{Command: "site.start", Description: "Запустить остановленный сайт", ParamsSchema: empty, ResultSchema: result(map[string]any{"site": site}, "site")},
		{Command: "site.database.set", Description: "Атомарно переключить сайт на восстановленную БД", ParamsSchema: closedSchema(map[string]any{"database_id": id, "expected_current_database_id": id}, []string{"database_id", "expected_current_database_id"}), ResultSchema: result(map[string]any{"site": site}, "site")},
		{Command: "disk.usage", Description: "Показать использование диска сервера", ParamsSchema: serverID, ResultSchema: diskUsageCatalogSchema(), TargetAuthRequired: true},
		{Command: "disk.cleanup", Description: "Удалить только очищаемые серверные логи", ParamsSchema: serverID, ResultSchema: result(map[string]any{"server_id": id, "freed_bytes": map[string]any{"type": "integer", "minimum": 0}, "usage": diskUsageCatalogSchema()}, "server_id", "freed_bytes", "usage"), TargetAuthRequired: true},
	}
	for index := range definitions {
		if definitions[index].Execution == "" {
			definitions[index].Execution = "synchronous"
		}
	}
	return definitions
}

func closedSchema(properties map[string]any, required []string) map[string]any {
	result := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func enumSchema(values ...string) map[string]any {
	items := make([]any, len(values))
	for index, value := range values {
		items[index] = value
	}
	return map[string]any{"type": "string", "enum": items}
}

func resourceIDCatalogSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`}
}

func arrayCatalogSchema(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func firewallRuleCatalogSchema() map[string]any {
	match := closedSchema(map[string]any{
		"source_cidr": map[string]any{"type": "string", "minLength": 1, "maxLength": 64},
		"region_code": map[string]any{"type": "string", "pattern": "^[A-Z]{2}$"},
		"user_agent":  closedSchema(map[string]any{"operator": enumSchema("equals", "contains"), "value": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048}}, []string{"operator", "value"}),
	}, nil)
	match["minProperties"] = 1
	return closedSchema(map[string]any{
		"rule_id":    resourceIDCatalogSchema(),
		"priority":   map[string]any{"type": "integer", "minimum": 0},
		"action":     enumSchema("allow", "deny"),
		"enabled":    map[string]any{"type": "boolean"},
		"match":      match,
		"expires_at": map[string]any{"type": "string", "format": "date-time"},
	}, []string{"rule_id", "priority", "action", "enabled", "match"})
}

func firewallRuleViewCatalogSchema() map[string]any {
	schema := firewallRuleCatalogSchema()
	properties := schema["properties"].(map[string]any)
	properties["revision"] = map[string]any{"type": "integer", "minimum": 1}
	schema["required"] = []string{"rule_id", "revision", "priority", "action", "enabled", "match"}
	return schema
}

func serverTypeCatalogSchema() map[string]any {
	return closedSchema(map[string]any{
		"instance_type": resourceIDCatalogSchema(), "role": enumSchema("backend", "database"),
		"capacity_units": map[string]any{"type": "integer", "minimum": 0}, "disk_bytes": map[string]any{"type": "integer", "minimum": 1},
		"connection_limit": map[string]any{"type": "integer", "minimum": 0}, "connection_hold_ms": map[string]any{"type": "integer", "minimum": 0},
		"cost_per_hour_minor": map[string]any{"type": "integer", "minimum": 0}, "cost_per_month_minor": map[string]any{"type": "integer", "minimum": 0},
		"provisioning_seconds": map[string]any{"type": "number", "minimum": 0},
	}, []string{"instance_type", "role", "capacity_units", "disk_bytes", "connection_limit", "connection_hold_ms", "cost_per_hour_minor", "cost_per_month_minor", "provisioning_seconds"})
}

func serverCatalogSchema() map[string]any {
	return closedSchema(map[string]any{
		"server_id": resourceIDCatalogSchema(), "name": map[string]any{"type": "string", "minLength": 1},
		"role": enumSchema("backend", "database"), "instance_type": resourceIDCatalogSchema(),
		"status":         enumSchema("provisioning", "active", "draining", "stopped", "failed"),
		"capacity_units": map[string]any{"type": "integer", "minimum": 0}, "used_load_units": map[string]any{"type": "integer", "minimum": 0},
		"cost_per_hour_minor": map[string]any{"type": "integer", "minimum": 0}, "disk": diskUsageCatalogSchema(),
		"database_ids": arrayCatalogSchema(resourceIDCatalogSchema()), "credential_id": resourceIDCatalogSchema(),
	}, []string{"server_id", "name", "role", "instance_type", "status", "capacity_units", "used_load_units", "cost_per_hour_minor", "disk", "database_ids", "credential_id"})
}

func databaseCatalogSchema() map[string]any {
	return closedSchema(map[string]any{
		"database_id": resourceIDCatalogSchema(), "server_id": resourceIDCatalogSchema(), "name": map[string]any{"type": "string", "minLength": 1},
		"status":     enumSchema("empty", "ready", "backing_up", "restoring", "unavailable"),
		"size_bytes": map[string]any{"type": "integer", "minimum": 0}, "data_version": map[string]any{"type": "integer", "minimum": 0},
		"capacity_units": map[string]any{"type": "integer", "minimum": 0}, "used_load_units": map[string]any{"type": "integer", "minimum": 0},
		"last_restored_backup_id": map[string]any{"type": []any{"string", "null"}},
	}, []string{"database_id", "server_id", "name", "status", "size_bytes", "data_version", "capacity_units", "used_load_units"})
}

func backupCatalogSchema() map[string]any {
	return closedSchema(map[string]any{
		"backup_id": resourceIDCatalogSchema(), "database_id": resourceIDCatalogSchema(), "source_server_id": resourceIDCatalogSchema(),
		"status": enumSchema("creating", "ready", "failed"), "data_version": map[string]any{"type": "integer", "minimum": 0},
		"size_bytes": map[string]any{"type": "integer", "minimum": 0}, "created_at": map[string]any{"type": "string", "format": "date-time"},
		"completed_at":                map[string]any{"type": []any{"string", "null"}, "format": "date-time"},
		"storage_cost_per_hour_minor": map[string]any{"type": "integer", "minimum": 0},
	}, []string{"backup_id", "database_id", "source_server_id", "status", "data_version", "size_bytes", "created_at", "storage_cost_per_hour_minor"})
}

func siteCatalogSchema() map[string]any {
	return closedSchema(map[string]any{"state": enumSchema("running", "stopping", "stopped"), "database_id": resourceIDCatalogSchema()}, []string{"state", "database_id"})
}

func diskUsageCatalogSchema() map[string]any {
	properties := map[string]any{}
	for _, field := range []string{"total_bytes", "system_bytes", "database_bytes", "logs_bytes", "used_bytes", "free_bytes", "cleanable_bytes"} {
		properties[field] = map[string]any{"type": "integer", "minimum": 0}
	}
	properties["server_id"] = map[string]any{"type": "string"}
	return closedSchema(properties, []string{"server_id", "total_bytes", "system_bytes", "database_bytes", "logs_bytes", "used_bytes", "free_bytes", "cleanable_bytes"})
}
