package simulation

import (
	"crypto/sha256"
	"fmt"

	"github.com/aigizk/hackersprint2-sim/internal/simulation/model"
)

// ServerCredentialID is deterministic metadata only. The corresponding
// username and password are generated independently in the secret repository.
func ServerCredentialID(runID string, serverID model.ServerID, version uint64) model.CredentialID {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", runID, serverID, version)))
	return model.CredentialID(fmt.Sprintf("cred-%x", digest[:12]))
}
