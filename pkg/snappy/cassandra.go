package snappy

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/errors"
)

var searchPaths = []string{
	"/etc/cassandra",
	"/etc/cassandra/conf",
	"/etc/dse/cassandra",
	"/etc/dse",
	"/usr/local/share/cassandra",
	"/usr/local/share/cassandra/conf",
	"/opt/cassandra",
	"/opt/cassandra/conf",
	"/usr/bin",
	"/usr/sbin",
}

type Cassandra struct {
	config map[string]interface{}
}

func find(filename string) string {
	for _, p := range searchPaths {
		var pathFilename = filepath.Join(p, filename)
		if _, err := os.Stat(pathFilename); err == nil {
			return pathFilename
		}
	}
	log.Fatal(filename, "was not found")
	return ""
}

func NewCassandra() *Cassandra {
	configFilename := cassandraYaml()

	config, err := parseYamlFile(configFilename)
	if err != nil {
		log.Fatal(err)
	}

	return &Cassandra{config: config}
}

func nodeTool() string {
	return find("nodetool")
}

func cassandraYaml() string {
	return find("cassandra.yaml")
}

// CreateSnapshotID generates a new monotic snapshot id
func (c *Cassandra) CreateSnapshotID() string {
	return time.Now().Format("2006-01-02_150405")
}

// CreateSnapshot creates a snapshot by ID
func (c *Cassandra) CreateSnapshot(id string) (bool, error) {
	nodeTool := nodeTool()
	log.Printf("creating a snapshot using id [%s]\n", id)
	cmd := exec.Command(nodeTool, "snapshot", "-t", id)

	if err := cmd.Start(); err != nil {
		return false, err
	}

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				if status.ExitStatus() == 2 {
					return false, errors.Errorf("snapshot already exists for [%s]", id)
				}
				return false, errors.Errorf("Exit Status: %d", status.ExitStatus())
			}
		} else {
			return false, errors.Errorf("cmd.Wait: %v", err)
		}
	}
	return true, nil
}

// GetDataDirectories returns a list of data directories defined in the config
func (c *Cassandra) GetDataDirectories() []string {
	var directories []string
	if dataDirs, ok := c.config["data_file_directories"]; ok {
		for _, dir := range dataDirs.([]interface{}) {
			directories = append(directories, dir.(string))
		}
	}
	return directories
}

func (c *Cassandra) GetSnapshotFiles(id string) map[string]string {

	var (
		keyspaces []string
		tables    []string
		s3Files   = make(map[string]string)
	)
	dataDirs := c.GetDataDirectories()
	node := c.GetListenAddress()

	for _, dataDir := range dataDirs {
		files, err := ioutil.ReadDir(dataDir)
		if err != nil {
			log.Fatal(err)
		}

		for _, file := range files {
			if file.IsDir() {
				keyspaces = append(keyspaces, file.Name())
			}
		}

		for _, keyspace := range keyspaces {

			files, err := ioutil.ReadDir(filepath.Join(dataDir, keyspace))
			if err != nil {
				log.Fatal(err)
			}

			for _, file := range files {
				if file.IsDir() {
					tables = append(tables, file.Name())
				}
			}

			for _, table := range tables {
				files, _ := filepath.Glob(fmt.Sprintf(filepath.Join(dataDir, keyspace, table, "snapshots", id, "*")))
				for _, file := range files {
					baseFile := filepath.Base(file)
					s3Files[file] = fmt.Sprintf("backups/%s/%s/%s/%s/%s", id, node, keyspace, table, baseFile)
				}
			}
		}
	}

	return s3Files
}

// GetListenAddress returns the listen_address from the config
func (c *Cassandra) GetListenAddress() string {
	if val, ok := c.config["listen_address"]; ok {
		return val.(string)
	}

	localIP, err := GetLocalIP()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("could not find a listen_address in cassandra.yaml, falling back to using %s\n", localIP)
	return localIP
}
