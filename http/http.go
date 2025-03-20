package http

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/abh/geodns/v3/appconfig"
	"github.com/abh/geodns/v3/monitor"
	"github.com/abh/geodns/v3/zones"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

type httpServer struct {
	application *gin.Engine
	zones       *zones.MuxManager
	serverInfo  *monitor.ServerInfo
	zonePath    string
}

func NewHTTPServer(mm *zones.MuxManager, serverInfo *monitor.ServerInfo, path string) *httpServer {
	application := gin.Default()
	hs := &httpServer{
		zones:       mm,
		application: application,
		serverInfo:  serverInfo,
		zonePath:    path,
	}
	authorized := hs.application.Group("/", hs.checkToken)

	authorized.GET("/zone", hs.getZones)
	authorized.GET("/zone/:zone", hs.getZone)
	authorized.POST("/zone/:zone", hs.addZone)

	return hs
}

func (hs *httpServer) checkToken(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Authorization header is missing",
		})
		return
	}
	token := strings.Replace(authHeader, "Bearer ", "", -1)
	if token != appconfig.Config.HTTP.Token {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"error":   "Unauthorized (401)",
		})
		return
	}
	c.Next()
}

func (hs *httpServer) Run(ctx context.Context, listen string) error {
	log.Println("Starting HTTP interface on", listen)

	srv := http.Server{
		Addr:         listen,
		Handler:      hs.application.Handler(),
		ReadTimeout:  5 * time.Second,
		IdleTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		err := srv.ListenAndServe()
		if err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		log.Printf("shutting down http server")
		return srv.Shutdown(ctx)
	})

	return g.Wait()
}

func (hs *httpServer) getZones(c *gin.Context) {

	zones := hs.zones.Zones()
	keys := slices.Collect(maps.Keys(zones))
	keys = slices.DeleteFunc(keys, func(key string) bool {
		return key == "pgeodns"
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  keys,
	})
}

func (hs *httpServer) getZone(c *gin.Context) {
	zoneName := c.Param("zone")
	zone := hs.zones.Zones()[zoneName]
	if zone != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"result":  zone,
		})
	} else {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Zone not found",
		})
	}
}

func (hs *httpServer) addZone(c *gin.Context) {
	zoneName := c.Param("zone")
	zone := hs.zones.Zones()[zoneName]
	if zone == nil {
		zone = zones.NewZone(zoneName)
	}
	var objmap map[string]interface{}
	if err := c.ShouldBindJSON(&objmap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	zone.ReadZoneJson(objmap)

	data, err := json.Marshal(objmap)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	err = os.WriteFile(hs.zonePath+zoneName+".json", data, 0644)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	hs.zones.AddHandler(zoneName, zone)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  "Zone created successfully",
	})
}
