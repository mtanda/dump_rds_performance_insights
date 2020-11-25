CREATE EXTERNAL TABLE getresourcemetrics (
  AlignedStartTime string,
  AlignedEndTime string,
  Identifier string,
  MetricList array<
    struct<
      Key: struct<
        Metric: string,
        Dimensions: map<string, string>
      >,
      DataPoints: array<
        struct<
          Timestamp: string,
          Value: double
        >
      >
    >
  >
)
PARTITIONED BY (accountId string, region string, dbInstanceIdentifier string, metric string, dimension string, dt string)
ROW FORMAT SERDE 'org.openx.data.jsonserde.JsonSerDe'
WITH SERDEPROPERTIES ('ignore.malformed.json' = 'true')
LOCATION 's3://rds-performance-insights-123456789012/GetResourceMetrics/';
MSCK REPAIR TABLE getresourcemetrics;
SELECT identifier, date_parse(dps.timestamp, '%Y-%m-%dT%H:%i:%SZ') AS timestamp, dps.value, ml.key.metric, ml.key.dimensions FROM getresourcemetrics
  CROSS JOIN UNNEST(metriclist) AS t(ml)
  CROSS JOIN UNNEST(ml.datapoints) AS t(dps)
  WHERE ml.key.dimensions['db.wait_event_type.name'] = 'CPU';

CREATE EXTERNAL TABLE describedimensionkeys (
  AlignedStartTime string,
  AlignedEndTime string,
  Keys array<
    struct<
      Dimensions: map<string, string>,
      Partitions: array<int>,
      Total: int
    >
  >,
  PartitionKeys array<
    struct<
      Dimensions: map<string, string>
    >
  >
)
PARTITIONED BY (accountId string, region string, dbInstanceIdentifier string, metric string, dimension string, dt string)
ROW FORMAT SERDE 'org.openx.data.jsonserde.JsonSerDe'
WITH SERDEPROPERTIES ('ignore.malformed.json' = 'true')
LOCATION 's3://rds-performance-insights-123456789012/DescribeDimensionKeys/';
MSCK REPAIR TABLE describedimensionkeys;
