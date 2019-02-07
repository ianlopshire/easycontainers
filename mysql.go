package easycontainers

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"strconv"
	"time"
)

// MySQL is a container using the official mysql docker image.
//
// Path is a path to a sql file, relative to the GOPATH. If set, it will run the sql in
// the file when initializing the container.
//
// Query is a string of SQL. If set, it will run the sql when initializing the container.
type MySQL struct {
	ContainerName string
	Port          int
	Path          string
	Query         string
}

// NewMySQL returns a new instance of MySQL and the port it will be using, which is
// a randomly selected number between 5000-6000.
//
// Conflicts are possible because it doesn't check if the port is already allocated.
func NewMySQL(name string) (r *MySQL, port int) {
	port = 5000 + rand.Intn(1000)

	return &MySQL{
		ContainerName: "mysql-" + name,
		Port:          port,
	}, port
}

// NewMySQLWithPort returns a new instance of MySQL using the specified port.
func NewMySQLWithPort(name string, port int) *MySQL {
	return &MySQL{
		ContainerName: "mysql-" + name,
		Port:          port,
	}
}

// Container spins up the mysql container and runs. When the method exits, the
// container is stopped and removed.
func (m *MySQL) Container(f func() error) error {
	containers[m.ContainerName] = struct{}{}

	CleanupContainer(m.ContainerName) // catch containers that previous cleanup missed
	defer CleanupContainer(m.ContainerName)

	var cmdList []*exec.Cmd

	runContainerCmd := exec.Command(
		"docker",
		"run",
		"--rm",
		"-p",
		fmt.Sprintf("%d:3306", m.Port),
		"--name",
		m.ContainerName,
		"-e",
		"MYSQL_ROOT_PASSWORD=pass",
		"-d",
		"mysql:5.5",
	)
	cmdList = append(cmdList, runContainerCmd)

	var sql string

	if m.Path != "" {
		b, err := ioutil.ReadFile(path.Join(GoPath(), m.Path))
		if err != nil {
			return err
		}

		sql = string(b)
	}

	if m.Query != "" {
		// the semicolon is in case the sql variable wasn't empty and the
		// previous sql string didn't end with a semicolon
		sql += "; " + m.Query
	}

	if sql != "" {
		fileName := strconv.Itoa(1+rand.Intn(1000)) + ".sql"

		file, err := os.Create(fileName)
		if err != nil {
			return err
		}

		// we create the table mysql.z_z_(id integer) after all the other sql has been run
		// so that we can query the table to see if all the startup sql is finished running,
		// which means that the container if fully initialized
		_, err = io.Copy(file, bytes.NewBufferString(sql+";CREATE TABLE mysql.z_z_(id integer);"))
		if err != nil {
			return err
		}

		err = file.Close()
		if err != nil {
			return err
		}

		defer os.Remove(fileName)

		addStartupSQLFileCmd := exec.Command(
			"/bin/bash",
			"-c",
			fmt.Sprintf(
				`docker cp %s $(docker ps --filter="name=%s" --format="{{.ID}}"):/docker-entrypoint-initdb.d`,
				fileName,
				m.ContainerName,
			),
		)
		cmdList = append(cmdList, addStartupSQLFileCmd)
	}

	waitForInitializeCmd := strCmdForContainer(
		m.ContainerName,
		"until (mysql -uroot -ppass -e 'select \"initialization table found\" from mysql.z_z_ limit 1') do echo 'waiting for mysql to be up'; sleep 1; done; sleep 3;",
	)
	cmdList = append(cmdList, waitForInitializeCmd)

	for _, c := range cmdList {
		err := RunCommandWithTimeout(c, 1*time.Minute)
		if err != nil {
			return err
		}
	}

	fmt.Println("successfully created mysql container")

	return f()
}
