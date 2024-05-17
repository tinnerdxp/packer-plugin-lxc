// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lxc

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
)

type stepExport struct{}

func (s *stepExport) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)

	ui := state.Get("ui").(packersdk.Ui)

	name := config.ContainerName

	lxc_dir := "/var/lib/lxc"
	user, err := user.Current()
	if err != nil {
		log.Print("Cannot find current user. Falling back to /var/lib/lxc...")
	}
	if user.Uid != "0" && user.HomeDir != "" {
		lxc_dir = filepath.Join(user.HomeDir, ".local", "share", "lxc")
	}

	containerDir := filepath.Join(lxc_dir, name)
	outputPath := filepath.Join(config.OutputDir, "rootfs.tar.gz")
	configFilePath := filepath.Join(config.OutputDir, "lxc-config")

	configFile, err := os.Create(configFilePath)

	if err != nil {
		err := fmt.Errorf("Error creating config file: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	configFileContents, err := os.ReadFile(config.ConfigFile)
	if err != nil {
		err := fmt.Errorf("Error opening config file: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	var perms []string = []string{}
	for _, line := range strings.Split(string(configFileContents), "\n") {
		if strings.Contains(line, "lxc.idmap") {
			r := strings.Split(line, "=")
			i := strings.Split(strings.TrimSpace(r[1]), " ")
			// log.Println("I:", i, "COUNT", len(i))

			if len(i) != 4 {
				err := fmt.Errorf("Error parsing idmap: \"%s\"", line)
				log.Fatal(err)
				state.Put("error", err)
				ui.Error(err.Error())
				return multistep.ActionHalt
			}
			perms = append(perms, "-m")
			perms = append(perms, fmt.Sprintf("%s:%s:%s:%s", i[0], i[1], i[2], i[3]))
		}
	}

	if len(perms) == 0 {
		err := fmt.Errorf("Error parsing idmap to create a command to wrap tar in lxc-usernsexec")
		log.Fatal(err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	originalConfigFile, err := os.Open(config.ConfigFile)
	if err != nil {
		err := fmt.Errorf("Error opening config file: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	_, err = io.Copy(configFile, originalConfigFile)
	if err != nil {
		err := fmt.Errorf("error copying file %s: %v", config.ConfigFile, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	
	commands := make([][]string, 4)
	commands[0] = []string{
		"lxc-stop", "--name", name,
	}
	commands[1] = []string{
		"sudo", "chmod", "-R", "0777", config.OutputDir,
		// "sudo", "setfacl", "-R", "-m", fmt.Sprintf("u:%s:rwx", user.Uid), filepath.Join(containerDir, "rootfs"),
		// "sudo", "setfacl", "-R", "-m", fmt.Sprintf("u:%s:rwx", "1000000"), filepath.Join(containerDir, "rootfs"),
	}

	commands[2] = func() []string {
		var cmd []string = []string{
			"lxc-usernsexec",
		}
		cmd = append(cmd, perms...)
		cmd = append(cmd, "--", "tar", "-C", fmt.Sprintf("%s/rootfs", containerDir), "--numeric-owner", "--anchored", "--exclude=./rootfs/dev/log", "-czf", outputPath, "./")
		return cmd
	}()
	
	// command[2] = []string{
	// 				"lxc-usernsexec", perms..., "--", "tar", "-C", fmt.Sprintf("%s/rootfs", containerDir), "--numeric-owner", "--anchored", "--exclude=./rootfs/dev/log", "-czf", outputPath, "./",
	// }
	commands[3] = []string{
		"chmod", "+x", configFilePath,
	}

	ui.Say("Exporting container...")
	for _, command := range commands {
		err := RunCommand(command...)
		if err != nil {
			err := fmt.Errorf("Error exporting container: %s, command: %s", err, command)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}
	return multistep.ActionContinue
}

func (s *stepExport) Cleanup(state multistep.StateBag) {}
