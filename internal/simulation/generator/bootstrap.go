package generator

import (
	"fmt"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/events"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

func (g *Generator) generateBootstrap(random deterministicRandom, at time.Time) []events.Event {
	result := []events.Event{
		events.InboxMessageDelivered{MessageID: "service-brief-001", SenderEmail: "sre@service.example", SentAt: at,
			Subject: "Эксплуатация сервиса", Description: "Цель: uptime не ниже 99% при минимальных расходах на инфраструктуру. Каталог доступен только для чтения."},
		events.InfrastructureConfigured{ServerProvisioningDuration: time.Duration(g.profile.Infrastructure.ServerProvisioningSeconds) * time.Second, ConfiguredAt: at},
		events.CostsConfigured{Currency: g.profile.Costs.Currency, ConfiguredAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend,
			DiskBytes: g.profile.Infrastructure.DatabaseDiskXBytes, BackendCapacityUnits: g.profile.Infrastructure.ServerCapacityUnits,
			CostPerHourMinor: g.profile.Infrastructure.ServerCostPerHourMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase,
			DiskBytes: g.profile.Infrastructure.DatabaseDiskXBytes, ConnectionLimit: model.DBSmallConnections, ConnectionHold: model.DBConnectionHold,
			CostPerMonthMinor: model.DBSmallCostPerMonthMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBMedium, Role: model.ServerRoleDatabase,
			DiskBytes: 2 * g.profile.Infrastructure.DatabaseDiskXBytes, ConnectionLimit: model.DBMediumConnections, ConnectionHold: model.DBConnectionHold,
			CostPerMonthMinor: model.DBMediumCostPerMonthMinor, DefinedAt: at},
		events.ServerTypeDefined{InstanceType: model.InstanceDBLarge, Role: model.ServerRoleDatabase,
			DiskBytes: 4 * g.profile.Infrastructure.DatabaseDiskXBytes, ConnectionLimit: model.DBLargeConnections, ConnectionHold: model.DBConnectionHold,
			CostPerMonthMinor: model.DBLargeCostPerMonthMinor, DefinedAt: at},
	}
	for _, page := range []model.PageType{model.PageProductList, model.PageProduct} {
		config := g.profile.Infrastructure.Pages[string(page)]
		result = append(result, events.PageConfigured{Page: page,
			LoadUnits:    random.int64Range(config.LoadUnits, "page:"+string(page)+":load"),
			HoldDuration: time.Duration(random.int64Range(config.ResourceHoldSeconds, "page:"+string(page)+":hold")) * time.Second,
			BaseLatency:  time.Duration(random.int64Range(config.BaseLatencyMS, "page:"+string(page)+":latency")) * time.Millisecond, ConfiguredAt: at})
	}
	for index := 1; index <= g.profile.Infrastructure.InitialBackendInstances; index++ {
		serverID := model.ServerID(fmt.Sprintf("server-initial-%d", index))
		operationID := model.OperationID("initial-" + string(serverID))
		result = append(result,
			events.ServerProvisioningStarted{OperationID: operationID, ServerID: serverID, Name: string(serverID), InstanceType: model.InstanceBackendStandard, Role: model.ServerRoleBackend,
				CapacityUnits: g.profile.Infrastructure.ServerCapacityUnits,
				DiskBytes:     g.profile.Infrastructure.DatabaseDiskXBytes, CostPerHourMinor: g.profile.Infrastructure.ServerCostPerHourMinor, StartedAt: at, ReadyAt: at},
			events.ServerActivated{OperationID: operationID, ServerID: serverID, ActivatedAt: at})
	}
	databaseServerID := model.ServerID("db-server-initial")
	databaseOperationID := model.OperationID("initial-" + string(databaseServerID))
	result = append(result,
		events.ServerProvisioningStarted{OperationID: databaseOperationID, ServerID: databaseServerID, Name: string(databaseServerID), InstanceType: model.InstanceDBSmall, Role: model.ServerRoleDatabase,
			DiskBytes: g.profile.Infrastructure.DatabaseDiskXBytes, ConnectionLimit: model.DBSmallConnections, ConnectionHold: model.DBConnectionHold,
			CostPerMonthMinor: model.DBSmallCostPerMonthMinor, StartedAt: at, ReadyAt: at},
		events.ServerActivated{OperationID: databaseOperationID, ServerID: databaseServerID, ActivatedAt: at},
		events.DatabaseCreated{DatabaseID: "db-main", ServerID: databaseServerID, Name: "service-main", CreatedAt: at},
		events.DatabaseGrowthRequested{GrowthID: "database-initial", DataDeltaBytes: g.profile.Infrastructure.InitialDatabaseDataBytes,
			LogsDeltaBytes: g.profile.Infrastructure.InitialDatabaseLogsBytes, RequestedAt: at},
		events.DatabaseStorageIncreased{GrowthID: "database-initial", DatabaseID: "db-main", ServerID: databaseServerID,
			DataBytesAdded: g.profile.Infrastructure.InitialDatabaseDataBytes, LogsBytesAdded: g.profile.Infrastructure.InitialDatabaseLogsBytes,
			DataVersion: 1, IncreasedAt: at},
		events.SiteStopStarted{OperationID: "initial-site-stop", StartedAt: at},
		events.SiteStopped{OperationID: "initial-site-stop", StoppedAt: at},
		events.SiteDatabaseChanged{DatabaseID: "db-main", DataVersion: 1, ChangedAt: at},
		events.SiteStarted{DatabaseID: "db-main", StartedAt: at},
	)
	count := int(random.int64Range(g.profile.Catalog.ProductCount, "catalog:product_count"))
	weights := make([]int64, count)
	var totalWeight int64
	for index := range weights {
		weights[index] = random.int64Range(g.profile.Catalog.PopularityWeight, fmt.Sprintf("product:%d:popularity", index+1))
		totalWeight += weights[index]
	}
	probabilities := distributeProbability(g.profile.Journey.CatalogToProductProbabilityPPM, weights, totalWeight)
	manufacturers := []string{"Acme", "Globex", "Initech", "Umbrella"}
	for index := 0; index < count; index++ {
		name := g.profile.Catalog.NameTemplates[index%len(g.profile.Catalog.NameTemplates)] + fmt.Sprintf(" %03d", index+1)
		result = append(result, events.ProductAdded{ProductID: model.ProductID(fmt.Sprintf("product-%03d", index+1)),
			Name: name, Description: "Описание товара " + name, Manufacturer: manufacturers[index%len(manufacturers)],
			PriceMinor: random.int64Range(g.profile.Catalog.PriceMinor, fmt.Sprintf("product:%d:price", index+1)),
			Available:  true, Version: 1, ViewProbabilityPPM: probabilities[index], AddedAt: at})
	}
	return result
}
