// Copyright 2016 SMFS Inc. DBA GRIMM. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

// Implementation of an OCSP responder defined by RFC 6960
package responder

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"gocsp-responder/crypto/ocsp"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"
)

var db *sql.DB

type OCSPResponder struct {
	IndexFile    string
	RespKeyFile  string
	RespCertFile string
	CaCertFile   string
	LogFile      string
	LogToStdout  bool
	Strict       bool
	Port         int
	Address      string
	Ssl          bool
	IndexEntries []IndexEntry
	IndexModTime time.Time
	CaCert       *x509.Certificate
	RespCert     *x509.Certificate
	NonceList    [][]byte
	Database	 bool
	MySQLcfg	 *mysql.Config
}

// I decided on these defaults based on what I was using
func Responder() *OCSPResponder {
	return &OCSPResponder{
		IndexFile:    "index.txt",
		RespKeyFile:  "responder.key",
		RespCertFile: "responder.crt",
		CaCertFile:   "ca.crt",
		LogFile:      "/var/log/gocsp-responder.log",
		LogToStdout:  false,
		Strict:       false,
		Port:         8888,
		Address:      "0.0.0.0",
		Ssl:          false,
		IndexEntries: nil,
		IndexModTime: time.Time{},
		CaCert:       nil,
		RespCert:     nil,
		NonceList:    nil,
		Database:	  false,
		MySQLcfg:	  mysql.NewConfig(),
	}
}

// Initialize `ocsp_index` table
func DbInitialize() error {
	query := `CREATE TABLE IF NOT EXISTS ocsp_index(
				serial BIGINT PRIMARY KEY,
				distinguished_name TEXT,
				valid_from DATETIME DEFAULT CURRENT_TIMESTAMP,
				valid_until DATETIME,
				revoked_status TINYINT DEFAULT 0,
				revoked_on DATETIME NULL,
				revocation_reason enum(
					'UNSPECIFIED',
					'KEYCOMPROMISE',
					'CACOMPROMISE',
					'AFFILIATIONCHANGED',
					'SUPERSEDED',
					'CESSATIONOFOPERATION',
					'CERTIFICATEHOLD',
					'REMOVEFROMCRL',
					'PRIVILEGEWITHDRAWN',
					'AACOMPROMISE'
				) NULL
			  );`
	_, err := db.Query(query)
	return err
}

// function to conncet to the database
func (self *OCSPResponder) DbConnect() error {
	// Get a database handle.
    var err error
    db, err = sql.Open("mysql", self.MySQLcfg.FormatDSN())
    if err != nil {
		return err
    }

    pingErr := db.Ping()
    if pingErr != nil {
		return pingErr
    }
    fmt.Println("Connected!")

	// Initialize database if OCSP table does not exist
	_, table_check := db.Query("select * from ocsp_index;")
	if table_check != nil {
		err := DbInitialize()
		return err
	}

	return nil
}

// Creates an OCSP http handler and returns it
func (self *OCSPResponder) makeHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		// Generate an access log
		log.Println(r.Host, r.RemoteAddr, r.Header["X-Forwarded-For"], r.Method, r.URL.Path,
			r.Header["Content-Length"], r.Header["User-Agent"])

		if self.Strict && r.Header.Get("Content-Type") != "application/ocsp-request" {
			log.Println("Strict mode requires correct Content-Type header")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		b := new(bytes.Buffer)
		switch r.Method {
		case "POST":
			b.ReadFrom(r.Body)
		case "GET":
			log.Println(r.URL.Path)
			gd, err := base64.StdEncoding.DecodeString(r.URL.Path[1:])
			if err != nil {
				log.Println(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r := bytes.NewReader(gd)
			b.ReadFrom(r)
		default:
			log.Println("Unsupported request method")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// parse request, verify, create response
		w.Header().Set("Content-Type", "application/ocsp-response")
		resp, err := self.verify(b.Bytes())
		if err != nil {
			log.Print(err)
			// technically we should return an ocsp error response. but this is probably fine
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		//log.Print("Writing response")
		w.Write(resp)
	}
}

// I only know of two types, but more can be added later
const (
	StatusValid   = 'V'
	StatusRevoked = 'R'
	StatusExpired = 'E'
)

type IndexEntry struct {
	Status byte
	Serial *big.Int
	ExpirationTime    time.Time
	RevocationTime    time.Time
	RevocationReason  string
	DistinguishedName string
}

func GetCertStatusFromDB(serial *big.Int) (IndexEntry, error) {
	var revokedStatus bool
	indexEntry := IndexEntry{}
	indexEntry.Serial = serial

	row := db.QueryRow("SELECT `distinguished_name`, `valid_until`, `revoked_status`, `revoked_on`, `revocation_reason` FROM `ocsp_index` WHERE serial = ?;", serial.String())
	err := row.Scan(&indexEntry.DistinguishedName, &indexEntry.ExpirationTime, &revokedStatus, &indexEntry.RevocationTime, &indexEntry.RevocationReason)
	if err != nil {
        if err == sql.ErrNoRows {
            return indexEntry, fmt.Errorf("Cannot find serial %d in database", serial)
        } else {
			fmt.Println(err)
		}
    }

	if revokedStatus == true {
		indexEntry.Status = StatusRevoked
	} else if time.Now().After(indexEntry.RevocationTime) {
		indexEntry.Status = StatusExpired
	} else {
		indexEntry.Status = StatusValid
	}
	return indexEntry, nil
}

// function to parse the index file
func (self *OCSPResponder) parseIndex() error {
	var t string = "060102150405Z"
	finfo, err := os.Stat(self.IndexFile)
	if err == nil {
		// if the file modtime has changed, then reload the index file
		if finfo.ModTime().After(self.IndexModTime) {
			log.Print("Index has changed. Updating")
			self.IndexModTime = finfo.ModTime()
			// clear index entries
			self.IndexEntries = self.IndexEntries[:0]
		} else {
			// the index has not changed. just return
			return nil
		}
	} else {
		return err
	}

	// open and parse the index file
	if file, err := os.Open(self.IndexFile); err == nil {
		defer file.Close()
		s := bufio.NewScanner(file)
		for s.Scan() {
			var ie IndexEntry
			ln := strings.Split(s.Text(), "\t")
			ie.Status = []byte(ln[0])[0]
			ie.ExpirationTime, _ = time.Parse(t, ln[1])
			ie.Serial, _ = new(big.Int).SetString(ln[3], 16)
			ie.DistinguishedName = ln[5]

			if ie.Status == StatusValid {
				ie.RevocationTime = time.Time{} //doesn't matter
				ie.RevocationReason = ""
			} else if ie.Status == StatusExpired {
				ie.RevocationTime = time.Time{} //doesn't matter
				ie.RevocationReason = ""
			} else if ie.Status == StatusRevoked {
				rr := strings.Split(ln[2], ",")
				ie.RevocationTime, _ = time.Parse(t, rr[0])
				ie.RevocationReason = rr[1]
			} else {
				// invalid status or bad line. just carry on
				continue
			}
			self.IndexEntries = append(self.IndexEntries, ie)
		}
	} else {
		return err
	}
	return nil
}

// updates the index if necessary and then searches for the given index in the
// index list
func (self *OCSPResponder) getIndexEntry(s *big.Int) (*IndexEntry, error) {
	log.Println(fmt.Sprintf("Looking for serial 0x%x", s))
	if err := self.parseIndex(); err != nil {
		return nil, err
	}
	for _, ent := range self.IndexEntries {
		if ent.Serial.Cmp(s) == 0 {
			return &ent, nil
		}
	}
	return nil, errors.New(fmt.Sprintf("Serial 0x%x not found", s))
}

// parses a pem encoded x509 certificate
func parseCertFile(filename string) (*x509.Certificate, error) {
	ct, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(ct)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return cert, nil
}

// parses a PEM encoded PKCS8 private key (RSA only)
func parseKeyFile(filename string) (interface{}, error) {
	kt, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(kt)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// takes a list of extensions and returns the nonce extension if it is present
func checkForNonceExtension(exts []pkix.Extension) *pkix.Extension {
	nonce_oid := asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 2}
	for _, ext := range exts {
		if ext.Id.Equal(nonce_oid) {
			//log.Println("Detected nonce extension")
			return &ext
		}
	}
	return nil
}

func (self *OCSPResponder) verifyIssuer(req *ocsp.Request) error {
	h := req.HashAlgorithm.New()
	h.Write(self.CaCert.RawSubject)
	if bytes.Compare(h.Sum(nil), req.IssuerNameHash) != 0 {
		return errors.New("Issuer name does not match")
	}
	h.Reset()
	var publicKeyInfo struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}
	if _, err := asn1.Unmarshal(self.CaCert.RawSubjectPublicKeyInfo, &publicKeyInfo); err != nil {
		return err
	}
	h.Write(publicKeyInfo.PublicKey.RightAlign())
	if bytes.Compare(h.Sum(nil), req.IssuerKeyHash) != 0 {
		return errors.New("Issuer key hash does not match")
	}
	return nil
}

// takes the der encoded ocsp request, verifies it, and creates a response
func (self *OCSPResponder) verify(rawreq []byte) ([]byte, error) {
	var status int
	var revokedAt time.Time
	var revokedReason int

	// parse the request
	req, exts, err := ocsp.ParseRequest(rawreq)
	//req, err := ocsp.ParseRequest(rawreq)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	//make sure the request is valid
	if err := self.verifyIssuer(req); err != nil {
		log.Println(err)
		return nil, err
	}

	// get the index entry, if it exists
	var entStore IndexEntry
	var ent *IndexEntry
	if self.Database {
		entStore, err = GetCertStatusFromDB(req.SerialNumber)
		ent = &entStore
	} else {
		ent, err = self.getIndexEntry(req.SerialNumber)
	}
	if err != nil {
		log.Println(err)
		status = ocsp.Unknown
	} else {
		log.Print(fmt.Sprintf("Found entry %+v", ent))
		if ent.Status == StatusRevoked {
			log.Print("This certificate is revoked")
			status = ocsp.Revoked
			revokedAt = ent.RevocationTime
			// Switch based on RevocationReason
			switch strings.ToUpper(ent.RevocationReason) {
			case "UNSPECIFIED":
				revokedReason = ocsp.Unspecified
			case "KEYCOMPROMISE":
				revokedReason = ocsp.KeyCompromise
			case "CACOMPROMISE":
				revokedReason = ocsp.CACompromise
			case "AFFILIATIONCHANGED":
				revokedReason = ocsp.AffiliationChanged
			case "SUPERSEDED":
				revokedReason = ocsp.Superseded
			case "CESSATIONOFOPERATION":
				revokedReason = ocsp.CessationOfOperation
			case "CERTIFICATEHOLD":
				revokedReason = ocsp.CertificateHold
			case "REMOVEFROMCRL":
				revokedReason = ocsp.RemoveFromCRL
			case "PRIVILEGEWITHDRAWN":
				revokedReason = ocsp.PrivilegeWithdrawn
			case "AACOMPROMISE":
				revokedReason = ocsp.AACompromise
			default:
				revokedReason = ocsp.Unspecified
			}
		} else if ent.Status == StatusExpired {
			log.Print("This certificate is expired")
			status = ocsp.Good
		} else if ent.Status == StatusValid {
			log.Print("This certificate is valid")
			status = ocsp.Good
		}
	}

	// parse key file
	// perhaps I should zero this out after use
	keyi, err := parseKeyFile(self.RespKeyFile)
	if err != nil {
		return nil, err
	}
	key, ok := keyi.(crypto.Signer)
	if !ok {
		return nil, errors.New("Could not make key a signer")
	}

	// check for nonce extension
	var responseExtensions []pkix.Extension
	nonce := checkForNonceExtension(exts)

	// check if the nonce has been used before
	if self.NonceList == nil {
		self.NonceList = make([][]byte, 10)
	}

	if nonce != nil {
		for _, n := range self.NonceList {
			if bytes.Compare(n, nonce.Value) == 0 {
				return nil, errors.New("This nonce has already been used")
			}
		}

		self.NonceList = append(self.NonceList, nonce.Value)
		responseExtensions = append(responseExtensions, *nonce)
	}

	// construct response template
	rtemplate := ocsp.Response{
		Status:           status,
		SerialNumber:     req.SerialNumber,
		Certificate:      self.RespCert,
		RevocationReason: revokedReason,
		IssuerHash:       req.HashAlgorithm,
		RevokedAt:        revokedAt,
		ThisUpdate:       time.Now().AddDate(0, 0, -1).UTC(),
		//adding 1 day after the current date. This ocsp library sets the default date to epoch which makes ocsp clients freak out.
		NextUpdate: time.Now().AddDate(0, 0, 1).UTC(),
		Extensions: exts,
	}

	// make a response to return
	resp, err := ocsp.CreateResponse(self.CaCert, self.RespCert, rtemplate, key)
	if err != nil {
		return nil, err
	}

	return resp, err
}

// setup an ocsp server instance with configured values
func (self *OCSPResponder) Serve() error {
	// setup logging
	if !self.LogToStdout {
		lf, err := os.OpenFile(self.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0664)
		if err != nil {
			log.Fatal("Could not open log file " + self.LogFile)
		}
		defer lf.Close()
		log.SetOutput(lf)
	}

	//the certs should not change, so lets keep them in memory
	cacert, err := parseCertFile(self.CaCertFile)
	if err != nil {
		log.Fatal(err)
		return err
	}
	respcert, err := parseCertFile(self.RespCertFile)
	if err != nil {
		log.Fatal(err)
		return err
	}

	self.CaCert = cacert
	self.RespCert = respcert

	if self.Database {
		self.DbConnect()
	}

	// Add handler for "/health"
	http.HandleFunc("/health", healthHandler)

	// get handler and serve
	handler := self.makeHandler()
	http.HandleFunc("/", handler)
	listenOn := fmt.Sprintf("%s:%d", self.Address, self.Port)
	log.Println(fmt.Sprintf("GOCSP-Responder starting on %s with SSL:%t", listenOn, self.Ssl))

	if self.Ssl {
		http.ListenAndServeTLS(listenOn, self.RespCertFile, self.RespKeyFile, nil)
	} else {
		http.ListenAndServe(listenOn, nil)
	}
	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	// Generate an access log
//	log.Println(r.Host, r.RemoteAddr, r.Header["X-Forwarded-For"], r.Method, r.URL.Path,
//		r.Header["Content-Length"], r.Header["User-Agent"])

	// Switch based on method
	switch r.Method {
	case "GET":
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}
