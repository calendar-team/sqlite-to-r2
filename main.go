package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/mattn/go-sqlite3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const BACKUP_FILE = "/tmp/backup.db3"

var (
	state = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sqlite_to_r2_backup_state",
		Help: "The state of the db backup. (1 - successful, 0 - failed)",
	})
	last_successful = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sqlite_to_r2_backup_last_successful",
		Help: "The last successful backup timestamp",
	})
	duration = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sqlite_to_r2_backups_duration_ms",
		Help: "The duration of db backup execution in ms",
	})
)

func main() {
	ctx := context.Background()
	bucketName, ok := os.LookupEnv("BUCKET_NAME")
	if !ok {
		log.Fatal("BUCKET_NAME env variable must be set")
	}

	accountId, ok := os.LookupEnv("ACCOUNT_ID")
	if !ok {
		log.Fatal("ACCOUNT_ID env variable must be set")
	}

	accessKeyId, ok := os.LookupEnv("ACCESS_KEY_ID")
	if !ok {
		log.Fatal("ACCESS_KEY_ID env variable must be set")
	}

	accessKeySecret, ok := os.LookupEnv("ACCESS_KEY_SECRET")
	if !ok {
		log.Fatal("ACCESS_KEY_SECRET env variable must be set")
	}

	dbFilePath, ok := os.LookupEnv("DB_FILE_PATH")
	if !ok {
		log.Fatal("DB_FILE_PATH env variable must be set")
	}

	ticker := time.NewTicker(3 * time.Hour)

	go func() {
		log.Print("Initializing R2 client")
		cfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyId, accessKeySecret, "")),
			config.WithRegion("auto"),
		)
		if err != nil {
			log.Fatal("Error while initializing R2 client: ", err)
		}

		s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountId))
		})

		for {
			func() {
				initialTime := time.Now()
				log.Print("Starting a new backup at ", initialTime)
				log.Print("Creating local sql db backup")
				err = backup(dbFilePath)
				if err != nil {
					state.Set(0)
					log.Print("Failed to create the backup, retrying in 2 seconds. The error was: ", err)
					time.Sleep(2 * time.Second)
					return
				}
				log.Print("Finished local sql db backup successfully")
				log.Print("Uploading backup to R2")
				err = upload(ctx, s3Client, bucketName)
				if err != nil {
					state.Set(0)
					log.Print("Failed to upload file to R2, retrying 2 seconds. The error was: ", err)
					time.Sleep(2 * time.Second)
					return
				}
				log.Print("Finished uploading to R2 successfully")
				endTime := time.Now()
				execDuration := endTime.Sub(initialTime)
				duration.Set(float64(execDuration.Milliseconds()))
				state.Set(1)
				last_successful.Set(float64(time.Now().Unix()))
				log.Print("Finished backup successfully in ", execDuration)
				_ = <-ticker.C
			}()
		}
	}()

	log.Print("Initializing the server on port 3333")
	http.Handle("/metrics", promhttp.Handler())
	err := http.ListenAndServe(":3333", nil)
	if err != nil {
		log.Fatal("failed initializing server at port 3333", err)
	}
}

func backup(sourceDbFile string) (err error) {
	srcDb, err := sql.Open("sqlite3", sourceDbFile+"?mode=ro")
	if err != nil {
		return fmt.Errorf("openning source database: %w", err)
	}
	defer srcDb.Close()
	err = srcDb.Ping()
	if err != nil {
		return fmt.Errorf("connecting to source database: %w", err)
	}
	srcConn, err := conn(srcDb)
	if err != nil {
		return fmt.Errorf("obtaining a connection to source database: %w", err)
	}

	os.Remove(BACKUP_FILE)
	destDb, err := sql.Open("sqlite3", BACKUP_FILE)
	if err != nil {
		return fmt.Errorf("openning destination database: %w", err)
	}
	defer destDb.Close()
	err = destDb.Ping()
	if err != nil {
		return fmt.Errorf("connecting to destination database: %w", err)
	}
	destConn, err := conn(destDb)
	if err != nil {
		return fmt.Errorf("obtaining a connection to destination database: %w", err)
	}

	backup, err := destConn.Backup("main", srcConn, "main")
	if err != nil {
		return fmt.Errorf("calling the backup sqlite API: %w", err)
	}

	if _, err = backup.Step(-1); err != nil {
		return fmt.Errorf("calling the step sqlite API: %w", err)
	}

	if err = backup.Finish(); err != nil {
		return fmt.Errorf("closing backup: %w", err)
	}
	return nil
}

func conn(db *sql.DB) (c *sqlite3.SQLiteConn, err error) {
	var rawConn *sql.Conn
	if rawConn, err = db.Conn(context.Background()); err != nil {
		return
	}

	err = rawConn.Raw(func(driverConn any) error {
		var ok bool
		if c, ok = driverConn.(*sqlite3.SQLiteConn); !ok {
			return errors.New("failed to get sqlite3 connection")
		}
		return nil
	})

	return
}

func upload(ctx context.Context, s3Client *s3.Client, bucketName string) (err error) {
	file, err := os.Open(BACKUP_FILE)
	if err != nil {
		return errors.New(fmt.Sprintf("couldn't open file %v to upload. Here's why: %v\n", BACKUP_FILE, err))
	}
	defer file.Close()

	objectKey := "backup.db3"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
		Body:   file,
	})

	if err != nil {
		return errors.New(fmt.Sprintf("couldn't upload file %v to %v:%v. Here's why: %v\n", BACKUP_FILE, bucketName, objectKey, err))
	}

	return nil
}
