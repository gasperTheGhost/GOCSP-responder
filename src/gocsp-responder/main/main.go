// Copyright 2016 SMFS Inc DBA GRIMM. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.
package main

import (
	"log"
	"flag"
	"gocsp-responder/responder"
	"github.com/go-sql-driver/mysql"
)

func main() {
	var verbose bool
	resp := responder.Responder()
	cfg := mysql.NewConfig()
	cfg.AllowNativePasswords = true
	cfg.ParseTime = true
	flag.StringVar(&resp.IndexFile, "index", getStringEnv("OCSP_INDEX", resp.IndexFile), "CA index filename")
	flag.StringVar(&resp.CaCertFile, "cacert", getStringEnv("OCSP_CACERT", resp.CaCertFile), "CA certificate filename")
	flag.StringVar(&resp.RespCertFile, "rcert", getStringEnv("OCSP_RESPCERT", resp.RespCertFile), "responder certificate filename")
	flag.StringVar(&resp.RespKeyFile, "rkey", getStringEnv("OCSP_RESPKEY", resp.RespKeyFile), "responder key filename")
	flag.StringVar(&resp.LogFile, "logfile", getStringEnv("OCSP_LOGFILE", resp.LogFile), "file to log to")
	flag.StringVar(&resp.Address, "bind", getStringEnv("OCSP_ADDRESS", resp.Address), "bind address")
	flag.BoolVar(&resp.Database, "mysql", getBoolEnv("OCSP_MYSQL", false), "use MySQL instead of textfile index")
	flag.StringVar(&cfg.User, "db-user", getStringEnv("OCSP_DB_USER", cfg.User), "database user")
	flag.StringVar(&cfg.Passwd, "db-pass", getStringEnv("OCSP_DB_PASS", cfg.Passwd), "database password")
	flag.StringVar(&cfg.Net, "db-protocol", getStringEnv("OCSP_DB_PROTOCOL", cfg.Net), "database connection protocol (tcp or unix)")
	flag.StringVar(&cfg.Addr, "db-address", getStringEnv("OCSP_DB_ADDRESS", cfg.Addr), "database address")
	flag.StringVar(&cfg.DBName, "db-name", getStringEnv("OCSP_DB_NAME", cfg.DBName), "database name")
	flag.IntVar(&resp.Port, "port", getIntEnv("OCSP_PORT", resp.Port), "listening port")
	flag.BoolVar(&resp.Ssl, "ssl", getBoolEnv("OCSP_SSL", resp.Ssl), "use SSL, this is not widely supported and not recommended")
	flag.BoolVar(&resp.Strict, "strict", getBoolEnv("OCSP_STRICT", resp.Strict), "require content type HTTP header")
	flag.BoolVar(&resp.LogToStdout, "stdout", getBoolEnv("OCSP_LOGTOSTDOUT", resp.LogToStdout), "log to stdout, not the log file")
	flag.BoolVar(&verbose, "verbose", false, "print configuration")
	flag.Parse()

	if (resp.Database) {
		resp.MySQLcfg = cfg
	}

	if (verbose) {
		if (resp.Database) {
			log.Println("Index database:", cfg.DBName)
			log.Println("DB Protocol:", cfg.Net)
			log.Println("DB User:", cfg.User)
			log.Println("DB Address:", cfg.Addr)
		} else {
			log.Println("Index file:", resp.IndexFile)
		}
		log.Println("CA Cert file:", resp.CaCertFile)
		log.Println("Responder Cert file:", resp.RespCertFile)
		log.Println("Responder Key file:", resp.RespKeyFile)
		log.Println("Log file:", resp.LogFile)
		log.Println("Bind address:", resp.Address)
		log.Println("Listen port:", resp.Port)
		log.Println("Use SSL:", resp.Ssl)
		log.Println("Strict:", resp.Strict)
		log.Println("Log to STDOUT:", resp.LogToStdout)
	}
	resp.Serve()
}
