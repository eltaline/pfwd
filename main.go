/*
 * Copyright © 2022 Andrey Kuvshinov. Contacts: <syslinux@protonmail.com>
 * Copyright © 2022 Eltaline OU. Contacts: <eltaline.ou@gmail.com>
 *
 * This file is part of pfwd.
 *
 * pfwd is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * pfwd is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"flag"
	"fmt"
	"github.com/gookit/config/v2"
	"github.com/gookit/config/v2/yaml"
	"github.com/gookit/validate"
	"github.com/rs/zerolog"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Global variables

var (

	// Config

	configfile string = "/etc/pfwd/pfwd.yaml"

	// Variables

	shutdown bool = false
	schannel = make(chan int, 1)

	tracemode bool = false
	debugmode bool = false
	testmode  bool = false

	loglevel string = "warn"

	logdir  string      = "/var/log/pfwd"
	logmode os.FileMode = 0640

	pidfile string = "/run/pfwd/pfwd.pid"

	esettings = make(map[string]string)
)

// Main function

func main() {

	var err error

	var version string = "1.0.0"
	var vprint bool = false
	var help bool = false

	// Command line options

	flag.StringVar(&configfile, "config", configfile, "--config=/etc/pfwd/pfwd.yaml")
	flag.BoolVar(&tracemode, "trace", tracemode, "--trace - trace mode")
	flag.BoolVar(&debugmode, "debug", debugmode, "--debug - debug mode")
	flag.BoolVar(&testmode, "test", testmode, "--test - test mode")
	flag.BoolVar(&vprint, "version", vprint, "--version - print version")
	flag.BoolVar(&help, "help", help, "--help - displays help")

	flag.Parse()

	switch {
	case vprint:
		fmt.Printf("pfwd Version: %s\n", version)
		os.Exit(0)
	case help:
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Load configuration

	// config.WithOptions(config.ParseEnv)

	config.AddDriver(yaml.Driver)

	err = config.LoadFiles(configfile)
	if err != nil {
		fmt.Printf("Can`t decode config file | File [%s] | %v\n", configfile, err)
		os.Exit(1)
	}

	// fmt.Printf("config data: \n %#v\n", config.Data())

	// Validate configuration

	v := validate.Map(config.Data())
	v.StringRule("tracemode", "bool")
	v.StringRule("debugmode", "bool")
	v.StringRule("pidfile", "required|string|unixPath")
	v.StringRule("loglevel", "required|string|in:trace,debug,info,warn,error,fatal,panic")
	v.StringRule("logdir", "required|string|unixPath")
	v.StringRule("logmode", "required|uint")

	if !v.Validate() {
		fmt.Println(v.Errors)
		os.Exit(1)
	}

	for cpfwd, msettings := range config.Get("forwards").(map[interface{}]interface{}) {

		pfwdOptions := make(map[string]interface{}, len(msettings.(map[interface{}]interface{})))
		for key, val := range msettings.(map[interface{}]interface{}) {
			pfwdOptions[key.(string)] = val
		}

		cpfwdName := make(map[string]interface{})
		cpfwdName["cpfwd"] = cpfwd.(string)

		v := validate.Map(cpfwdName)
		v.StringRule("cpfwd", "string")

		if !v.Validate() {
			fmt.Println(v.Errors)
			os.Exit(1)
		}

		v = validate.Map(pfwdOptions)
		v.StringRule("dst", "required|string")

		if !v.Validate() {
			fmt.Println(v.Errors)
			os.Exit(1)
		}

	}

	// Test mode

	if testmode {
		os.Exit(0)
	}

	// Logging

	loglevel = config.String("loglevel")
	logdir = filepath.Clean(config.String("logdir"))
	logmode = os.FileMode(config.Uint("logmode"))

	logfile := filepath.Clean(logdir + "/" + "app.log")
	applogfile, err := os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logmode)
	if err != nil {
		fmt.Printf("Can`t open/create app log file | File [%s] | %v\n", logfile, err)
		os.Exit(1)
	}
	defer applogfile.Close()

	err = os.Chmod(logfile, logmode)
	if err != nil {
		fmt.Printf("Can`t chmod log file | File [%s] | %v\n", logfile, err)
		os.Exit(1)
	}

	switch loglevel {
	case "trace":
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	}

	if debugmode {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	if tracemode {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	}

	zerolog.TimeFieldFormat = "02/Jan/2006:15:04:05"
	appLogger := zerolog.New(applogfile).With().Timestamp().Logger()

	// System handling

	// Get pid

	gpid, fpid := GetPID()

	// Pid file

	pidfile = filepath.Clean(config.String("pidfile"))

	switch {
	case FileExists(pidfile):

		err = os.Remove(pidfile)
		if err != nil {
			appLogger.Error().Msgf("Can`t remove pid file | File [%s] | %v", pidfile, err)
			fmt.Printf("Can`t remove pid file | File [%s] | %v\n", pidfile, err)
			os.Exit(1)
		}

		fallthrough

	default:

		err = ioutil.WriteFile(pidfile, []byte(fpid), 0644)
		if err != nil {
			appLogger.Error().Msgf("Can`t create pid file | File [%s] | %v", pidfile, err)
			fmt.Printf("Can`t create pid file | File [%s] | %v\n", pidfile, err)
			os.Exit(1)
		}

	}

	appLogger.Info().Msgf("Starting pfwd service [%s]", version)

	appLogger.Info().Msgf("Trace mode: [%t]", tracemode)
	appLogger.Info().Msgf("Debug mode: [%t]", debugmode)

	appLogger.Info().Msgf("Pid file: [%s]", pidfile)

	appLogger.Info().Msgf("Log level: [%s]", loglevel)
	appLogger.Info().Msgf("Log directory: [%s]", logdir)
	appLogger.Info().Msgf("Log mode: [%v]", logmode)

	// Populate path settings

	for cpfwd, msettings := range config.Get("forwards").(map[interface{}]interface{}) {

		scpfwd := cpfwd.(string)

		appLogger.Info().Msgf("Source: [%s]", scpfwd)

		for key, val := range msettings.(map[interface{}]interface{}) {

			if key == "dst" {
				esettings[scpfwd] = val.(string)
			}

		}

	}

	if len(esettings) == 0 {
		appLogger.Error().Msgf("No have any configured forwards | %v", esettings)
		fmt.Printf("No have any configured forwards | %v\n", esettings)
		os.Exit(1)
	}

	// Main code

	for src, dst := range esettings {

		ln, err := net.Listen("tcp", src)
		if err != nil {

			appLogger.Error().Msgf("Can`t listen on address | Address [%s] | %v", src, err)

			if debugmode {
				fmt.Printf("Can`t listen on address | Address [%s] | %v\n", src, err)
			}

			os.Exit(1)

		}

		go func(ln net.Listener, src string, dst string) {

		Accept:

			for {
				select {
				case <-schannel:
					schannel <- 1
					break Accept
				default:
				}

				type accepted struct {
					cnn net.Conn
					err error
				}

				cchannel := make(chan accepted, 1)

				go func() {

					s, err := ln.Accept()
					if err != nil {

						if !shutdown {

							appLogger.Error().Msgf("Can`t accept on address | Address [%s] | %v", src, err)

							if debugmode {
								fmt.Printf("Can`t accept on address | Address [%s] | %v\n", src, err)
							}

						}

					}

					cchannel <- accepted{s, err}

				}()

				select {
				case s := <-cchannel:
					if s.err != nil {
						continue Accept
					}

					go func(s net.Conn, dst string) {

						appLogger.Info().Msgf("Connection from | Address [%s]", s.RemoteAddr())

						d, err := net.Dial("tcp", dst)
						appLogger.Info().Msgf("Connection from | Address [%s]", s.RemoteAddr())
						if err != nil {

							appLogger.Error().Msgf("Can`t dial to address | Address [%s] | %v", dst, err)

							if debugmode {
								fmt.Printf("Can`t dial to address | Address [%s] | %v\n", dst, err)
							}

							return

						}

						appLogger.Info().Msgf("Connected to | Address [%s]", dst)

						// Initiate continoous communication

						go Forward(s, d)
						go Forward(d, s)

					}(s.cnn, dst)

				case <-schannel:
					schannel <- 1
					ln.Close()
					break Accept
				}

			}

		}(ln, src, dst)

	}

	appLogger.Info().Msgf("pfwd service running with a pid: %s", gpid)

	// Daemon channel

	// done := make(chan bool)

	// Interrupt handler

	InterruptHandler := func() {

		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM|syscall.SIGKILL)

		<-c

		shutdown = true
		schannel <- 1

		// Fixed shutdown timeout

		time.Sleep(5 * time.Second)

		appLogger.Info().Msgf("Finished all go routines")

		// Shutdown message

		appLogger.Info().Msgf("Shutdown pfwd service completed")

		// Remove pid file

		if FileExists(pidfile) {
			err = os.Remove(pidfile)
			if err != nil {
				appLogger.Error().Msgf("Can`t remove pid file error | File [%s] | %v", pidfile, err)
				fmt.Printf("Can`t remove pid file error | File [%s] | %v\n", pidfile, err)
				// os.Exit(1)
			}
		}

		// done <- true
		os.Exit(0)

	}

	// Interrupt routine

	InterruptHandler()

	// Daemon channel

	// <-done

}
