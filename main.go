package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"
	"errors"
	"io/ioutil"
	"github.com/brianvoe/gofakeit/v6"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types" 
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/matoous/go-nanoid/v2"
)

func generateRandomSentence() string {

	n, _ := rand.Int(rand.Reader, big.NewInt(100))

	gofakeit.Seed(n.Int64())

	sentence := gofakeit.Sentence(10)

	fmt.Printf("the message is generated: %s\n", sentence)

	return sentence
}


func encryptMessage(kmsClient *kms.Client, kmsArn string, message string) (string, error) {
	input := &kms.EncryptInput{
		KeyId:     aws.String(kmsArn),
		Plaintext: []byte(message),
	}

	result, err := kmsClient.Encrypt(context.TODO(), input)
	if err != nil {
		return "", err
	}

	encryptedMessage := base64.StdEncoding.EncodeToString(result.CiphertextBlob)
	return encryptedMessage, nil
}


func writeToStorage(s3Client *s3.Client, dynamoClient *dynamodb.Client, arn string, message string) error {
	if strings.HasPrefix(arn, "arn:aws:s3") {
		bucketName := strings.Split(arn, ":")[5]

		file := &s3.GetObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String("index.html"),
		}

		existingContent := ""
		result, err := s3Client.GetObject(context.TODO(), file)
		if err != nil {
			var nsk *s3types.NoSuchKey
			if !errors.As(err, &nsk) {
				return fmt.Errorf("failed to get object: %v", err)
			} 
		}else {
			body, err := ioutil.ReadAll(result.Body)
			if err != nil {
				return fmt.Errorf("failed to read file content: %v", err)
			}
			existingContent = string(body)
			defer result.Body.Close()
		}

		fmt.Printf("current s3 content is : %s\n", existingContent)

		newMessage := existingContent + "\n" + message

		_, err = s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String("index.html"),
			Body:   strings.NewReader(newMessage),
			ContentType: aws.String("text/html"),
		})
		if err != nil {
			return fmt.Errorf("failed to upload file: %v", err)
		}
		return nil
	} else if strings.HasPrefix(arn, "arn:aws:dynamodb") {

		tableName := strings.ReplaceAll(strings.Split(arn, ":")[5], "table/", "")
		id, _ := gonanoid.New(10)
		//fmt.Printf("uuid is : %s\n", id)
		item := map[string]dynamotypes.AttributeValue{
			"id": &dynamotypes.AttributeValueMemberS{Value: id},
			"message": &dynamotypes.AttributeValueMemberS{Value: message},
		}
		
		_, err := dynamoClient.PutItem(context.TODO(), &dynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: item,
		})
		return err
	}

	return fmt.Errorf("invalid storage ARN")
}


func writeToCloudWatchLog(cwClient *cloudwatchlogs.Client, logGroupArn string, encryptedMessage string, logStream string,) error {
	logGroupName := strings.Split(logGroupArn, ":")[6]
	logStreamName := logStream
	
	_, err := cwClient.CreateLogStream(context.TODO(), &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(logStreamName),
	})
	if err != nil {
		// Check if the error is because the log stream already exists
		var alreadyExists *cwtypes.ResourceAlreadyExistsException
		if !errors.As(err, &alreadyExists) {
			log.Printf("Error creating log stream: %v", err)
			return err
		}
	}
	
	timestamp := time.Now().UnixMilli()
	input := &cloudwatchlogs.PutLogEventsInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(logStreamName),
		LogEvents: []cwtypes.InputLogEvent{
			{
				Timestamp: aws.Int64(timestamp),
				Message:   aws.String(encryptedMessage),
			},
		},
	}

	_, err = cwClient.PutLogEvents(context.TODO(), input)
	return err
}

func main() {
	
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatalf("Unable to load AWS SDK config, %v", err)
	}

	
	logGroupArn := os.Getenv("AWS_LOG_GROUP_ARN")
	fmt.Printf("AWS CW LOG GROUP ARN IS: %s\n", logGroupArn)
	kmsArn := os.Getenv("AWS_KMS_ARN")
	fmt.Printf("AWS KMS ARN IS: %s\n", kmsArn)
	storageArn := os.Getenv("STORAGE_ARN")
	fmt.Printf("AWS STORAGE ARN IS: %s\n", storageArn)
	

	if logGroupArn == "" || kmsArn == "" || storageArn == "" {
		log.Fatalf("Please set AWS_LOG_GROUP_ARN, AWS_KMS_ARN, and STORAGE_ARN environment variables.")
	}

	kmsClient := kms.NewFromConfig(cfg)
	s3Client := s3.NewFromConfig(cfg)
	dynamoClient := dynamodb.NewFromConfig(cfg)
	cwClient := cloudwatchlogs.NewFromConfig(cfg)

	podNamespace := os.Getenv("MY_POD_NAMESPACE")
	podIp := os.Getenv("MY_POD_IP")

	logStreamName := podIp + "-POD-" + podNamespace

	for {
		// Generate a random sentence
		randomSentence := generateRandomSentence()

		// Encrypt the message using KMS
		encryptedMessage, err := encryptMessage(kmsClient, kmsArn, randomSentence)
		if err != nil {
			log.Fatalf("Failed to encrypt message: %v", err)
		}
		fmt.Printf("Hashed Encrypted message: %s\n", encryptedMessage)

		// Write the encrypted message to CloudWatch Logs
		err = writeToCloudWatchLog(cwClient, logGroupArn, encryptedMessage, logStreamName)
		if err != nil {
			log.Fatalf("Failed to write encrypted message to CloudWatch Logs: %v", err)
		}

		// Write the original message to storage
		err = writeToStorage(s3Client, dynamoClient, storageArn, randomSentence)
		if err != nil {
			log.Fatalf("Failed to write message to storage: %v", err)
		}
		
		n, _ := rand.Int(rand.Reader, big.NewInt(31))

		// Sleep for a random duration between 30-60 seconds
		sleepDuration := time.Duration(30+n.Int64()) * time.Second
		fmt.Printf("Sleeping for %v...\n", sleepDuration)
		time.Sleep(sleepDuration)
	}
}
