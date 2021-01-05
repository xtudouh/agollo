/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package remote

import (
	"fmt"
	. "github.com/tevid/gohamcrest"
	"github.com/xtudouh/agollo/v5/cluster/roundrobin"
	"github.com/xtudouh/agollo/v5/env"
	"github.com/xtudouh/agollo/v5/env/config"
	jsonFile "github.com/xtudouh/agollo/v5/env/file/json"
	"github.com/xtudouh/agollo/v5/env/server"
	"github.com/xtudouh/agollo/v5/extension"
	"github.com/xtudouh/agollo/v5/protocol/auth/sign"
	http2 "github.com/xtudouh/agollo/v5/protocol/http"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var asyncApollo *asyncApolloConfig

func init() {
	extension.SetLoadBalance(&roundrobin.RoundRobin{})
	extension.SetFileHandler(&jsonFile.FileHandler{})

	asyncApollo = &asyncApolloConfig{}
	asyncApollo.remoteApollo = asyncApollo
}

const configResponseStr = `{
  "appId": "100004458",
  "cluster": "default",
  "namespaceName": "application",
  "configurations": {
    "key1":"value1",
    "key2":"value2"
  },
  "releaseKey": "20170430092936-dee2d58e74515ff3"
}`

const configFilesResponseStr = `{
    "key1":"value1",
    "key2":"value2"
}`

const configAbc1ResponseStr = `{
  "appId": "100004458",
  "cluster": "default",
  "namespaceName": "abc1",
  "configurations": {
    "key1":"value1",
    "key2":"value2"
  },
  "releaseKey": "20170430092936-dee2d58e74515ff3"
}`

const responseStr = `[{"namespaceName":"application","notificationId":%d}]`

func onlyNormalConfigResponse(rw http.ResponseWriter, req *http.Request) {
	rw.WriteHeader(http.StatusOK)
	fmt.Fprintf(rw, configResponseStr)
}

func onlyNormalTwoConfigResponse(rw http.ResponseWriter, req *http.Request) {
	rw.WriteHeader(http.StatusOK)
	fmt.Fprintf(rw, configAbc1ResponseStr)
}

func onlyNormalResponse(rw http.ResponseWriter, req *http.Request) {
	result := fmt.Sprintf(responseStr, 3)
	fmt.Fprintf(rw, "%s", result)
}

func initMockNotifyAndConfigServer() *httptest.Server {
	//clear
	handlerMap := make(map[string]func(http.ResponseWriter, *http.Request), 1)
	handlerMap["application"] = onlyNormalConfigResponse
	handlerMap["abc1"] = onlyNormalTwoConfigResponse
	return runMockConfigServer(handlerMap, onlyNormalResponse)
}

//run mock config server
func runMockConfigServer(handlerMap map[string]func(http.ResponseWriter, *http.Request),
	notifyHandler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	appConfig := env.InitFileConfig()
	uriHandlerMap := make(map[string]func(http.ResponseWriter, *http.Request), 0)
	for namespace, handler := range handlerMap {
		uri := fmt.Sprintf("/configs/%s/%s/%s", appConfig.AppID, appConfig.Cluster, namespace)
		uriHandlerMap[uri] = handler
	}
	uriHandlerMap["/notifications/v2"] = notifyHandler

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri := r.RequestURI
		for path, handler := range uriHandlerMap {
			if strings.HasPrefix(uri, path) {
				handler(w, r)
				break
			}
		}
	}))

	return ts
}

func initNotifications() *config.AppConfig {
	appConfig := env.InitFileConfig()
	appConfig.NamespaceName = "application,abc1"
	appConfig.Init()
	return appConfig
}

//Error response
//will hold 5s and keep response 404
func runErrorResponse() *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	return ts
}

func TestApolloConfig_Sync(t *testing.T) {
	server := initMockNotifyAndConfigServer()
	appConfig := initNotifications()
	appConfig.IP = server.URL
	apolloConfigs := asyncApollo.Sync(func() config.AppConfig {
		return *appConfig
	})
	//err keep nil
	Assert(t, apolloConfigs, NotNilVal())
	Assert(t, len(apolloConfigs), Equal(2))
}

func TestToApolloConfigError(t *testing.T) {

	notified, err := toApolloConfig([]byte("jaskldfjaskl"))
	Assert(t, notified, NilVal())
	Assert(t, err, NotNilVal())
}

func TestGetRemoteConfig(t *testing.T) {
	server := initMockNotifyAndConfigServer()

	time.Sleep(1 * time.Second)

	var remoteConfigs []*config.Notification
	var err error
	appConfig := initNotifications()
	appConfig.IP = server.URL
	remoteConfigs, err = asyncApollo.notifyRemoteConfig(func() config.AppConfig {
		return *appConfig
	}, EMPTY)

	//err keep nil
	Assert(t, err, NilVal())

	Assert(t, remoteConfigs, NotNilVal())
	Assert(t, 1, Equal(len(remoteConfigs)))
	t.Log("remoteConfigs:", remoteConfigs)
	t.Log("remoteConfigs size:", len(remoteConfigs))

	notify := remoteConfigs[0]

	Assert(t, "application", Equal(notify.NamespaceName))
	Assert(t, true, Equal(notify.NotificationID > 0))
}

func TestErrorGetRemoteConfig(t *testing.T) {
	//clear
	initNotifications()
	appConfig := initNotifications()
	server1 := runErrorResponse()
	appConfig.IP = server1.URL
	server.SetNextTryConnTime(appConfig.GetHost(), 0)

	time.Sleep(1 * time.Second)

	var remoteConfigs []*config.Notification
	var err error

	remoteConfigs, err = asyncApollo.notifyRemoteConfig(func() config.AppConfig {
		return *appConfig
	}, EMPTY)

	Assert(t, err, NotNilVal())
	Assert(t, remoteConfigs, NilVal())
	Assert(t, 0, Equal(len(remoteConfigs)))
	t.Log("remoteConfigs:", remoteConfigs)
	t.Log("remoteConfigs size:", len(remoteConfigs))

	Assert(t, "over Max Retry Still Error", Equal(err.Error()))
}

func TestCreateApolloConfigWithJson(t *testing.T) {
	jsonStr := `{
  "appId": "100004458",
  "cluster": "default",
  "namespaceName": "application",
  "configurations": {
    "key1":"value1",
    "key2":"value2"
  },
  "releaseKey": "20170430092936-dee2d58e74515ff3"
}`
	o, err := createApolloConfigWithJSON([]byte(jsonStr), http2.CallBack{})
	c := o.(*config.ApolloConfig)

	Assert(t, err, NilVal())
	Assert(t, c, NotNilVal())

	Assert(t, "100004458", Equal(c.AppID))
	Assert(t, "default", Equal(c.Cluster))
	Assert(t, "application", Equal(c.NamespaceName))
	Assert(t, "20170430092936-dee2d58e74515ff3", Equal(c.ReleaseKey))
	Assert(t, "value1", Equal(c.Configurations["key1"]))
	Assert(t, "value2", Equal(c.Configurations["key2"]))

}

func TestCreateApolloConfigWithJsonError(t *testing.T) {
	jsonStr := `jklasdjflasjdfa`

	config, err := createApolloConfigWithJSON([]byte(jsonStr), http2.CallBack{})

	Assert(t, err, NotNilVal())
	Assert(t, config, NilVal())
}

func TestGetConfigURLSuffix(t *testing.T) {
	appConfig := &config.AppConfig{}
	appConfig.Init()
	uri := asyncApollo.GetSyncURI(*appConfig, "kk")
	Assert(t, "", NotEqual(uri))
}


/*
		Server:    "http://meta-server.dev.s.2345inc.com:8080",
		AppId:     "basic-api-demo-gomicro",
		Cluster:   "default",
		Secret:    "170cc48f511b41c0a9571ae88835bd71",
		BackupDir: "apollo-cache",
*/
func TestSync(t *testing.T) {
	async := new(asyncApolloConfig)
	async.remoteApollo = async
	extension.SetHTTPAuth(&sign.AuthSignature{})
	async.Sync(func () config.AppConfig {
		cfg := config.AppConfig{
			IP: "http://meta-server.dev.s.2345inc.com:8080",
			AppID: "basic-api-demo-gomicro",
			Cluster:   "default",
			Secret:    "170cc48f511b41c0a9571ae88835bd71",
			NamespaceName: "redis.json",
		}

		cfg.Init()
		return cfg
	})
}