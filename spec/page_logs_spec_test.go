package spec_test

import (
	"testing"
	"time"

	"github.com/aigizk/hackersprint2-sim/internal/simulation"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/logs"
	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
	"github.com/aigizk/hackersprint2-sim/spec"
)

func TestEveryPageOpeningWritesSuccessfulSiteLog(t *testing.T) {
	testCases := []struct {
		name      string
		page      model.PageType
		productID model.ProductID
	}{
		{name: "product list", page: model.PageProductList},
		{name: "product page", page: model.PageProduct, productID: "product-1"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			s := spec.New(t, "run-success-"+string(testCase.page))
			requestID := model.RequestID("request-" + string(testCase.page))

			s.Given(
				s.World.Created(42, worldStartsAt, worldStartsAt.Add(24*time.Hour)),
				s.Product.Added("product-1", "Coffee machine", 12_990, 850_000),
			)

			s.When(
				s.User.OpensPage(requestID, "visitor-1", testCase.page, testCase.productID),
			)

			s.Then(
				s.Logs.Exactly(logs.Entry{
					Timestamp:  worldStartsAt,
					RequestID:  requestID,
					Source:     model.RequestSourceVisitor,
					VisitorID:  "visitor-1",
					Page:       testCase.page,
					ProductID:  testCase.productID,
					SourceIP:   simulation.VisitorClientProfile(42, "visitor-1").SourceIP,
					UserAgent:  simulation.VisitorClientProfile(42, "visitor-1").UserAgent,
					RegionCode: simulation.VisitorClientProfile(42, "visitor-1").RegionCode,
					StatusCode: 200,
				}),
			)
		})
	}
}
