package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"gateway/internal/db"
	"gateway/internal/worker"
)

type JobPayload struct {
	TeamID  string `json:"team_id"`
	AssetID string `json:"asset_id"`
}

func RegisterWorker(processor *worker.Processor, service *Service, teamsDir string) {
	processor.Register("embedding", func(ctx context.Context, claim *worker.Claim) error {
		var payload JobPayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("decode embedding job: %w", err)
		}
		if payload.TeamID == "" {
			payload.TeamID = claim.TeamID
		}
		if payload.AssetID == "" {
			payload.AssetID = claim.AssetID
		}
		teamDB, err := db.OpenTeamDB(teamsDir, payload.TeamID)
		if err != nil {
			return err
		}
		defer teamDB.Close()
		asset, err := db.GetAsset(ctx, teamDB, payload.TeamID, payload.AssetID)
		if err != nil {
			return err
		}
		vector, err := service.EncodeDocument(asset.Name + "\n" + asset.Summary)
		if err != nil {
			return err
		}
		return db.UpdateAssetEmbedding(ctx, teamDB, payload.AssetID, Float32ToBytes(vector), service.ModelVersion())
	})
}

func Enqueue(ctx context.Context, queue *worker.Queue, teamID, assetID string) error {
	_, err := queue.Enqueue(ctx, worker.Job{Queue: "embedding", TeamID: teamID, AssetID: assetID, AssetType: "embedding", PartitionKey: "embedding:" + teamID + ":" + assetID, Payload: JobPayload{TeamID: teamID, AssetID: assetID}})
	return err
}
