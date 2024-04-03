package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func main() {
	var (
		origin_bucket_folder = "gs://dev-us-central1-cortex-tsdb-dev/9960"
		block_file_name      = "blocks.txt"
		maxline              = 0
		data_folder          = "./testbucket"
		dst_bucket_folder    = "gs://dev-us-central1-cortex-tsdb-dev/ying"
	)

	// crate log files
	logFile, err := os.OpenFile("logfile.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error create logfile:", err)
	}
	log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	blocks_file, err := os.Create(block_file_name)
	if err != nil {
		fmt.Println("the creation of blocks.txt failed")
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			fmt.Println("Error closing file:", err)
		}
		if cerr := blocks_file.Close(); cerr != nil {
			fmt.Println("Error closing file:", cerr)
		}
	}()

	log.Println("Program started")
	startTime := time.Now()
	block_folders := listBlocks(origin_bucket_folder, blocks_file, maxline)
	endTime := time.Now()
	log.Printf("Execution time of listBlocks: %v", endTime.Sub(startTime))
	fmt.Println("the total blocks would be rewritten are: ", len(block_folders))

	var copy_block_total_time, rewrite_block_total_time, upload_block_total_time, delete_block_total_time time.Time
	for _, block_folder := range block_folders {
		// get block_uid
		s := strings.Split(block_folder, "/")
		if len(s) < 2 {
			continue
		}
		block_uid := s[len(s)-2]

		// copy block in local
		startTime := time.Now()
		err = copyBlock(block_folder, data_folder)
		if err != nil {
			log.Println("Failed to download new block, the block_uid is ", block_uid)
			continue
		}
		endTime := time.Now()
		copy_block_total_time = copy_block_total_time.Add(endTime.Sub(startTime))
		log.Printf("Execution time of copy block %s: %v, total download block until now %v", block_folder, endTime.Sub(startTime), copy_block_total_time)

		// rewrite block with filter
		startTime = time.Now()
		new_block_uid, err := rewriteBlock(block_uid, data_folder)
		if err != nil {
			log.Println("Failed to write new block, the block_uid is ", block_uid)
			continue
		}
		endTime = time.Now()
		rewrite_block_total_time = rewrite_block_total_time.Add(endTime.Sub(startTime))
		log.Printf("Execution time of rewrite block %s: %v, total time for rewrite %v", block_folder, endTime.Sub(startTime), rewrite_block_total_time)

		// upload block to gcp
		startTime = time.Now()
		err = uploadBlock(new_block_uid, data_folder, dst_bucket_folder)
		if err != nil {
			log.Println("Failed to upload new block, the block_uid is ", new_block_uid)
			continue
		}
		endTime = time.Now()
		upload_block_total_time = upload_block_total_time.Add(endTime.Sub(startTime))
		log.Printf("Execution time of upload block %s: %v, total time for upload %v", block_folder, endTime.Sub(startTime), upload_block_total_time)

		// delete new old block in local
		startTime = time.Now()
		err = deleteBlock(data_folder, block_uid, new_block_uid)
		if err != nil {
			log.Println("Failed to delete block, the block_uids are ", block_uid, new_block_uid)
			continue
		}
		endTime = time.Now()
		delete_block_total_time = delete_block_total_time.Add(endTime.Sub(startTime))
		log.Printf("Execution time of delete block %s, %s: %v, total time for delete %v", block_uid, new_block_uid, endTime.Sub(startTime), delete_block_total_time)
	}
}

func deleteBlock(data_folder, block_uid, new_block_uid string) error {
	cmd := exec.Command("rm", "-rf", filepath.Join(data_folder, block_uid), filepath.Join(data_folder, new_block_uid))
	_, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("delete block %s and %s failed: %v", block_uid, new_block_uid, err)
	}
	return err
}

func rewriteBlock(block_uid string, data_folder string) (string, error) {
	log.Printf("the block_uid is %s, and data_folder is: %s", block_uid, data_folder)
	cmd := exec.Command("./thanos", "tools", "bucket", "rewrite", "--no-dry-run",
		"--id", block_uid,
		"--objstore.config-file", "./objstore_config.yml",
		"--rewrite.to-delete-config-file", "./matchers.yml",
	)
	// Run the command and capture its output
	output, err := cmd.CombinedOutput()

	if err != nil {
		fmt.Println("COMMAND is", cmd.String(), "Error:", err.Error())
		return "", nil
	}
	re := regexp.MustCompile(`new=([^ ]+)`)
	match := re.FindSubmatch(output)
	if len(match) > 1 {
		block_uid := match[1]
		return string(block_uid), nil
	}
	return "", fmt.Errorf("can't read new block id from the log")
}

func uploadBlock(block_uid string, data_folder string, dst_bucket_folder string) error {
	cmd := exec.Command("gsutil", "cp", "-r", filepath.Join(data_folder, block_uid), dst_bucket_folder)
	_, err := cmd.Output()
	if err != nil {
		log.Fatalf("upload block %s failed: %v", block_uid, err)
		return err
	}
	return nil
}

func copyBlock(block_folder string, data_folder string) error {
	cmd := exec.Command("gsutil", "cp", "-r", block_folder, data_folder)
	_, err := cmd.Output()
	if err != nil {
		log.Fatalf("download block %s failed: %v", block_folder, err)
	}
	return err
}

func listBlocks(bucket_folder string, blocks_file *os.File, maxline int) []string {
	// Command to list objects in the specified GCS bucket
	log.Printf("Listing blocks for bucket folder: %s", bucket_folder)
	cmd := exec.Command("gsutil", "ls", bucket_folder)
	blocks := []string{}
	// Execute the command
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error:", err)
		return blocks
	}

	// Regex pattern to match the desired format
	pattern := regexp.MustCompile(bucket_folder + `/01[[:alnum:]]*`)

	// Parse and print only the lines that match the regex pattern
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() && (maxline == 0 || len(blocks) < maxline) {
		line := scanner.Text()
		if pattern.MatchString(line) {
			blocks = append(blocks, line)
			blocks_file.Write([]byte(fmt.Sprintf("%s\n", line)))
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error scanning output:", err)
	}
	return blocks
}
