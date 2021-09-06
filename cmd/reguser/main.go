package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gbbackend1/reguser/api/handler"
	"github.com/gbbackend1/reguser/api/server"
	"github.com/gbbackend1/reguser/app/repos/user"
	"github.com/gbbackend1/reguser/app/starter"
	"github.com/gbbackend1/reguser/db/mem/usermemstore"
	"github.com/gbbackend1/reguser/db/pgstore"
)

func main() {
	if tz := os.Getenv("TZ"); tz != "" {
		var err error
		time.Local, err = time.LoadLocation(tz)
		if err != nil {
			log.Printf("error loading location '%s': %v\n", tz, err)
		}
	}

	// output current time zone
	tnow := time.Now()
	tz, _ := tnow.Zone()
	log.Printf("Local time zone %s. Service started at %s", tz,
		tnow.Format("2006-01-02T15:04:05.000 MST"))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)

	var ust user.UserStore
	if pgdsn := os.Getenv("DATABASE_URL"); pgdsn != "" {
		log.Println("use postgres at ", pgdsn)
		ust = pgstore.NewUsers(pgdsn)
	} else {
		ust = usermemstore.NewUsers()
	}
	a := starter.NewApp(ust)
	us := user.NewUsers(ust)
	h := handler.NewRouter(us)
	srv := server.NewServer(":"+os.Getenv("PORT"), h)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	go a.Serve(ctx, wg, srv)

	<-ctx.Done()
	cancel()
	wg.Wait()
}
