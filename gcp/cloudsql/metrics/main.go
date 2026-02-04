package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	ctx := context.Background()
	projectID := os.Getenv("PROJECT_ID")
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create MetricClient: %v", err)
	}
	defer client.Close()

	end := time.Now().UTC()
	start := end.Add(-10 * time.Minute)

	req := &monitoringpb.ListTimeSeriesRequest{
		Name: "projects/" + projectID,

		Filter: `metric.type="cloudsql.googleapis.com/database/replication/replica_lag"`,

		Interval: &monitoringpb.TimeInterval{
			StartTime: timestamppb.New(start),
			EndTime:   timestamppb.New(end),
		},

		Aggregation: &monitoringpb.Aggregation{
			AlignmentPeriod:  durationpb.New(60 * time.Second),
			PerSeriesAligner: monitoringpb.Aggregation_ALIGN_MEAN,
		},

		View: monitoringpb.ListTimeSeriesRequest_FULL,
	}

	it := client.ListTimeSeries(ctx, req)

	for {
		ts, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			panic(err)
		}

		dbID := ts.Resource.Labels["database_id"]
		region := ts.Resource.Labels["region"]

		fmt.Printf("=== Instance=%s Region=%s ===\n", dbID, region)

		for _, p := range ts.Points {
			fmt.Printf(
				"%s  lag=%.6f sec\n",
				p.Interval.EndTime.AsTime().Format(time.RFC3339),
				p.Value.GetDoubleValue(),
			)
		}
	}
}
