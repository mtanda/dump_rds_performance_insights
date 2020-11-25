package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/aws/aws-sdk-go/service/sts"
)

type LambdaRequest struct {
	Region   string `json:"region"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Interval string `json:"interval"`
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
		"db.sampledload.avg",
	}
	piDimensions = []string{
		//"db.user",
		"db.host",
		"db.sql",
		"db.sql_tokenized",
		"db.wait_event",
		"db.wait_event_type",
	}
)

func dump(region string, startTime time.Time, endTime time.Time, interval time.Duration, dumpType string) error {
	sess := session.Must(session.NewSession(&aws.Config{Region: aws.String(region)}))
	piSvc := pi.New(sess)
	rdsSvc := rds.New(sess)

	stsSvc := sts.New(sess)
	identity, err := stsSvc.GetCallerIdentity(&sts.GetCallerIdentityInput{})
	if err != nil {
		return err
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
		return err
	}

	for _, instance := range resp.DBInstances {
		if !*instance.PerformanceInsightsEnabled {
			continue
		}
		for _, piMetric := range piMetrics {
			for _, piDimension := range piDimensions {
				var buf bytes.Buffer
				writer := gzip.NewWriter(&buf)

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
							return err
						}

						b, err := json.Marshal(&resp)
						if err != nil {
							return err
						}
						if _, err := writer.Write(b); err != nil {
							return err
						}
						if _, err := writer.Write([]byte("\n")); err != nil {
							return err
						}

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
							Group: aws.String(piDimension),
							Limit: aws.Int64(limit),
						},
						StartTime:       aws.Time(startTime),
						EndTime:         aws.Time(endTime),
						PeriodInSeconds: aws.Int64(periodInSeconds),
						MaxResults:      aws.Int64(maxResults),
					})
					if err != nil {
						return err
					}

					b, err := json.Marshal(&resp)
					if err != nil {
						return err
					}
					if _, err := writer.Write(b); err != nil {
						return err
					}
					if _, err := writer.Write([]byte("\n")); err != nil {
						return err
					}
				}

				writer.Close()
				bucket := "rds-performance-insights-" + *identity.Account
				now := time.Now().Truncate(interval)
				key := fmt.Sprintf("%s/accountid=%s/region=%s/dbinstanceidentifier=%s/metric=%s/dimension=%s/dt=%s/%s.json.gz", dumpType, *identity.Account, region, *instance.DBInstanceIdentifier, piMetric, piDimension, now.Format("2006-01-02-15"), now.Format("20060102T150405Z"))
				uploader := s3manager.NewUploader(sess)
				_, err = uploader.Upload(&s3manager.UploadInput{
					Bucket: aws.String(bucket),
					Key:    aws.String(key),
					Body:   bytes.NewReader(buf.Bytes()),
				})
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func handler(req LambdaRequest) (LambdaResponse, error) {
	var startTime, endTime time.Time
	var d time.Duration
	var err error

	if req.Interval != "" {
		d, err = time.ParseDuration(req.Interval)
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

		now := time.Now().Truncate(d)
		startTime = now.Add(-d)
		endTime = now
	} else {
		d, err = time.ParseDuration("1s")
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
		startTime, err = time.Parse(time.RFC3339, req.Start)
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
		endTime, err = time.Parse(time.RFC3339, req.End)
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
	}
	dumpTypes := []string{"GetResourceMetrics", "DescribeDimensionKeys"}
	if req.DumpType != "" {
		dumpTypes = []string{req.DumpType}
	}
	for _, dumpType := range dumpTypes {
		err = dump(req.Region, startTime, endTime, d, dumpType)
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
		logger := log.New(os.Stderr, "", log.LstdFlags)

		now := time.Now()
		region := flag.String("region", "us-east-1", "region")
		start := flag.String("start", now.Add(-20*periodInSeconds*time.Second).Format(time.RFC3339), "start time")
		end := flag.String("end", now.Format(time.RFC3339), "end time")
		dumpType := flag.String("dump-type", "GetResourceMetrics", "dump type")
		flag.Parse()

		var startTime, endTime time.Time
		var err error
		startTime, err = time.Parse(time.RFC3339, *start)
		if err != nil {
			logger.Fatal(err)
		}
		endTime, err = time.Parse(time.RFC3339, *end)
		if err != nil {
			logger.Fatal(err)
		}
		d, err := time.ParseDuration("1s")
		if err != nil {
			logger.Fatal(err)
		}

		err = dump(*region, startTime, endTime, d, *dumpType)
		if err != nil {
			logger.Fatal(err)
		}
	}
}
