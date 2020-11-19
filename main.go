package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awsutil"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/pi"
	"github.com/aws/aws-sdk-go/service/rds"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
)

type LambdaRequest struct {
	Region   string `json:"region"`
	Start    string `json:"start"`
	End      string `json:"end"`
	DumpType string `json:"dumpType"`
}
type LambdaResponse struct {
	StatusCode        int               `json:"statusCode"`
	StatusDescription string            `json:"statusDescription"`
	Headers           map[string]string `json:"headers"`
	Body              string            `json:"body"`
	IsBase64Encoded   bool              `json:"isBase64Encoded"`
}

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

func dump(region string, start string, end string, dumpType string) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)

	sess := session.Must(session.NewSession(&aws.Config{Region: aws.String(region)}))
	piSvc := pi.New(sess)
	rdsSvc := rds.New(sess)
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		logger.Fatal(err)
	}
	endTime, err := time.Parse(time.RFC3339, end)
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
				switch dumpType {
				case "GetResourceMetrics":
					st := startTime
					for st.Before(endTime) {
						nt := st.Add(maxResults * periodInSeconds * time.Second)
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
							StartTime:       aws.Time(st),
							EndTime:         aws.Time(nt),
							PeriodInSeconds: aws.Int64(periodInSeconds),
							MaxResults:      aws.Int64(maxResults),
						})
						if err != nil {
							logger.Fatal(err)
						}
						fmt.Printf("%+v\n", resp)
						st = nt
						time.Sleep(1 * time.Second)
					}
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

	return nil
}

func handler(req LambdaRequest) (LambdaResponse, error) {
	err := dump(req.Region, req.Start, req.End, req.DumpType)
	if err != nil {
		return LambdaResponse{
			StatusCode:        500,
			StatusDescription: "500 Internal Server Error",
			IsBase64Encoded:   false,
			Headers: map[string]string{
				"Content-Type": "text/plain",
			},
			Body: "error",
		}, err
	}

	return LambdaResponse{
		StatusCode:        200,
		StatusDescription: "200 OK",
		IsBase64Encoded:   false,
		Headers: map[string]string{
			"Content-Type": "text/plain",
		},
		Body: "success",
	}, nil
}

func main() {
	if strings.HasPrefix(os.Getenv("AWS_EXECUTION_ENV"), "AWS_Lambda") || os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		lambda.Start(handler)
	} else {
		now := time.Now()
		region := flag.String("region", "us-east-1", "region")
		start := flag.String("start", now.Add(-20*periodInSeconds*time.Second).Format(time.RFC3339), "start time")
		end := flag.String("end", now.Format(time.RFC3339), "end time")
		dumpType := flag.String("dump-type", "GetResourceMetrics", "dump type")
		flag.Parse()
		dump(*region, *start, *end, *dumpType)
	}
}
