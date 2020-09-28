package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/pi"
	"github.com/aws/aws-sdk-go/service/rds"
)

const (
	periodInSeconds = 60
	maxResults      = 20
	limit           = 10
)

var (
	piMetrics = []string{
		"db.load.avg",
		//"db.sampledload.avg",
	}
	piDimensions = []string{
		//"db.user",
		//"db.host",
		//"db.sql",
		//"db.sql_tokenized",
		"db.wait_event",
		//"db.wait_event_type",
	}
)

func main() {
	now := time.Now()
	region := flag.String("region", "us-east-1", "region")
	start := flag.String("start", now.Add(-20*periodInSeconds*time.Second).Format(time.RFC3339), "start time")
	end := flag.String("end", now.Format(time.RFC3339), "end time")
	dumpType := flag.String("dump-type", "GetResourceMetrics", "dump type")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)

	sess := session.Must(session.NewSession(&aws.Config{Region: region}))
	piSvc := pi.New(sess)
	rdsSvc := rds.New(sess)
	startTime, err := time.Parse(time.RFC3339, *start)
	if err != nil {
		logger.Fatal(err)
	}
	endTime, err := time.Parse(time.RFC3339, *end)
	if err != nil {
		logger.Fatal(err)
	}

	var resp rds.DescribeDBInstancesOutput
	err = rdsSvc.DescribeDBInstancesPages(&rds.DescribeDBInstancesInput{},
		func(page *rds.DescribeDBInstancesOutput, lastPage bool) bool {
			instances, _ := awsutil.ValuesAtPath(page, "DBInstances")
			for _, instance := range instances {
				resp.DBInstances = append(resp.DBInstances, instance.(*rds.DBInstance))
			}
			return !lastPage
		})
	if err != nil {
		logger.Fatal(err)
	}

	for _, instance := range resp.DBInstances {
		if !*instance.PerformanceInsightsEnabled {
			continue
		}
		for _, piMetric := range piMetrics {
			for _, piDimension := range piDimensions {
				switch *dumpType {
				case "GetResourceMetrics":
					// TODO: loop for each time range
					resp, err := piSvc.GetResourceMetrics(&pi.GetResourceMetricsInput{
						ServiceType: aws.String("RDS"),
						Identifier:  instance.DbiResourceId,
						MetricQueries: []*pi.MetricQuery{
							&pi.MetricQuery{
								Metric: aws.String(piMetric),
								GroupBy: &pi.DimensionGroup{
									Group: aws.String(piDimension),
									Limit: aws.Int64(limit),
								},
							},
						},
						StartTime:       aws.Time(startTime),
						EndTime:         aws.Time(endTime),
						PeriodInSeconds: aws.Int64(periodInSeconds),
						MaxResults:      aws.Int64(maxResults),
					})
					if err != nil {
						logger.Fatal(err)
					}
					fmt.Printf("%+v\n", resp)
				case "DescribeDimensionKeys":
					resp, err := piSvc.DescribeDimensionKeys(&pi.DescribeDimensionKeysInput{
						ServiceType: aws.String("RDS"),
						Identifier:  instance.DbiResourceId,
						Metric:      aws.String(piMetric),
						GroupBy: &pi.DimensionGroup{
							Group: aws.String(piDimension),
							Limit: aws.Int64(limit),
						},
						PartitionBy: &pi.DimensionGroup{
							Group: aws.String("db.wait_event"),
							Limit: aws.Int64(limit),
						},
						StartTime:       aws.Time(startTime),
						EndTime:         aws.Time(endTime),
						PeriodInSeconds: aws.Int64(periodInSeconds),
						MaxResults:      aws.Int64(maxResults),
					})
					if err != nil {
						logger.Fatal(err)
					}
					fmt.Printf("%+v\n", resp)
				}
			}
		}
	}
}
