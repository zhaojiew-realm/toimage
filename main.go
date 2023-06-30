package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"

	"log"
	"net/http"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type Output struct {
	ImageName string
	Command   string
}

func main() {
	http.HandleFunc("/", index)
	http.HandleFunc("/upload", myrepo)
	log.Println("The server is listening on 0.0.0.0:5455")
	http.ListenAndServe(":5455", nil)
}

// index
func index(w http.ResponseWriter, r *http.Request) {
	t, _ := template.ParseFiles("/home/ec2-user/toimage/index.html")
	t.Execute(w, "hi")
}

// toimage
func myrepo(w http.ResponseWriter, r *http.Request) {
	t, _ := template.ParseFiles("/home/ec2-user/toimage/output.html")
	log.Println("recieving request")
	imagename := r.URL.Query().Get("iamgename")
	if imagename == "" {
		log.Println("Please input image name and tag")
		return
	}

	// Get account id
	sts_client := sts.New(session.New(), aws.NewConfig().WithRegion("cn-north-1"))
	input := &sts.GetCallerIdentityInput{}
	stsresult, err := sts_client.GetCallerIdentity(input)
	if err != nil {
		log.Println("Can not get account id")
		// log.Println("Can not get account id")
		return
	}
	accountid := *stsresult.Account
	log.Println("Your destination account is", accountid)

	//tag image
	parts := strings.Split(imagename, "/")
	tempstr := parts[len(parts)-1]
	split := strings.Split(tempstr, ":")
	shortname := split[0]
	tag := ""
	if len(split) == 2 {
		tag = split[1]
	} else {
		tag = "latest"
	}
	tagimage := accountid + ".dkr.ecr.cn-north-1.amazonaws.com.cn/" + shortname + ":" + tag
	log.Println("The image name is", tagimage)
	log.Println("login with")
	command := fmt.Sprintf("aws ecr get-login-password --region cn-north-1 | docker login --username AWS --password-stdin %s.dkr.ecr.cn-north-1.amazonaws.com.cn", accountid)
	log.Println(command)

	// output
	output := Output{
		ImageName: tagimage,
		Command:   command,
	}
	go lonetime(accountid, imagename, tagimage, shortname, tag)
	t.Execute(w, output)
}

func lonetime(accountid, imagename, tagimage, shortname, tag string) {
	// Initialize
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.WithVersion("1.41"))
	if err != nil {
		log.Println("Unable to create docker client")
		return
	}

	// pull image
	pullReader, pull_err := cli.ImagePull(ctx, imagename, types.ImagePullOptions{})
	if pull_err != nil {
		log.Printf("Failed to pull image '%s': %s\n", imagename, err.Error())
		return
	}
	defer pullReader.Close()
	io.Copy(os.Stdout, pullReader)
	log.Printf("Successcully pull image %s", imagename)

	cli.ImageTag(ctx, imagename, tagimage)

	// Create repo
	mySession := session.Must(session.NewSession())
	ecr_cli := ecr.New(mySession, aws.NewConfig().WithRegion("cn-north-1"))
	create_input := &ecr.CreateRepositoryInput{
		RepositoryName: aws.String(shortname),
	}
	ecrresult, err := ecr_cli.CreateRepository(create_input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecr.ErrCodeRepositoryAlreadyExistsException:
				log.Println(ecr.ErrCodeRepositoryAlreadyExistsException, aerr.Error())
			default:
				log.Println(aerr.Error())
			}
		} else {
			log.Println(err.Error())
		}
	} else {
		log.Println("Success creating repo", ecrresult.Repository.RepositoryArn)
	}
	log.Println("The repo name is", accountid+".dkr.ecr.cn-north-1.amazonaws.com.cn/"+shortname)

	// Push image
	ecr_client := ecr.New(session.New(), aws.NewConfig().WithRegion("cn-north-1"))
	token_input := &ecr.GetAuthorizationTokenInput{}
	result, err := ecr_client.GetAuthorizationToken(token_input)
	if err != nil {
		fmt.Println(err.Error())
	}
	token := *result.AuthorizationData[0].AuthorizationToken
	decodedToken, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		fmt.Println("Error decoding token:", err)
		return
	}
	passwd := strings.Split(string(decodedToken), ":")[1]
	authConfig := types.AuthConfig{
		Username: "AWS",
		Password: passwd,
	}
	encodedJSON, _ := json.Marshal(authConfig)
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)
	push_out, push_err := cli.ImagePush(context.TODO(), fmt.Sprintf("%s.dkr.ecr.cn-north-1.amazonaws.com.cn/%s:%s", accountid, shortname, tag), types.ImagePushOptions{RegistryAuth: authStr})
	if push_err != nil {
		log.Println("Unable to push image")
		return
	}
	io.Copy(os.Stdout, push_out)
	log.Println("Image pushed to ECR successfully")

	// Remove image
	_, removeerr1 := cli.ImageRemove(ctx, tagimage, types.ImageRemoveOptions{})
	if removeerr1 != nil {
		log.Println("Failed to remover image", err)
		return
	}
	_, removeerr2 := cli.ImageRemove(ctx, imagename, types.ImageRemoveOptions{})
	if removeerr2 != nil {
		log.Println("Failed to remover image", err)
		return
	}
	log.Println("Clear temp image successfully")
	// return info
	log.Println("The image is successful pushed")
}
