package pgstore

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/gbbackend1/reguser/app/repos/user"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v4/stdlib" // Postgresql driver
)

var _ user.UserStore = &Users{}

type DBPgUser struct {
	ID          uuid.UUID  `db:"id"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	DeletedAt   *time.Time `db:"deleted_at"`
	Name        string     `db:"name"`
	Data        string     `db:"data"`
	Permissions int        `db:"perms"`
}

type Users struct {
	db *sql.DB
}

func NewUsers(dsn string) *Users {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	err = db.Ping()
	if err != nil {
		db.Close()
		log.Fatal(err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS public.users (
		id uuid NOT NULL,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL,
		deleted_at timestamptz NULL,
		name varchar NOT NULL,
		"data" varchar NULL,
		perms int2 NULL,
		CONSTRAINT users_pk PRIMARY KEY (id)
	)`)
	if err != nil {
		db.Close()
		log.Fatal(err)
	}

	us := &Users{
		db: db,
	}
	return us
}

func (us *Users) Close() {
	us.db.Close()
}

func (us *Users) Create(ctx context.Context, u user.User) (*uuid.UUID, error) {
	uid := uuid.New()
	dbu := &DBPgUser{
		ID:          uid,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Name:        u.Name,
		Data:        u.Data,
		Permissions: u.Permissions,
	}

	_, err := us.db.ExecContext(ctx, `INSERT INTO users 
	(id, created_at, updated_at, deleted_at, name, data, perms)
	values ($1, $2, $3, $4, $5, $6, $7)`,
		dbu.ID,
		dbu.CreatedAt,
		dbu.UpdatedAt,
		nil,
		dbu.Name,
		dbu.Data,
		dbu.Permissions,
	)
	if err != nil {
		return nil, err
	}

	return &uid, nil
}

func (us *Users) Read(ctx context.Context, uid uuid.UUID) (*user.User, error) {
	dbu := &DBPgUser{}
	rows, err := us.db.QueryContext(ctx, `SELECT id, created_at, updated_at, deleted_at, name, data, perms 
	FROM users WHERE id = $1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(
			&dbu.ID,
			&dbu.CreatedAt,
			&dbu.UpdatedAt,
			&dbu.DeletedAt,
			&dbu.Name,
			&dbu.Data,
			&dbu.Permissions,
		); err != nil {
			return nil, err
		}
	}

	return &user.User{
		ID:          dbu.ID,
		Name:        dbu.Name,
		Data:        dbu.Data,
		Permissions: dbu.Permissions,
	}, nil
}

func (us *Users) Delete(ctx context.Context, uid uuid.UUID) error {
	_, err := us.db.ExecContext(ctx, `UPDATE users SET deleted_at = $2 WHERE id = $1`,
		uid, time.Now(),
	)
	return err
}

func (us *Users) SearchUsers(ctx context.Context, s string) (chan user.User, error) {
	chout := make(chan user.User, 100)

	go func() {
		defer close(chout)
		dbu := &DBPgUser{}

		rows, err := us.db.QueryContext(ctx, `
		SELECT id, created_at, updated_at, deleted_at, name, data, perms 
		FROM users WHERE name LIKE $1`, s+"%")
		if err != nil {
			log.Println(err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			if err := rows.Scan(
				&dbu.ID,
				&dbu.CreatedAt,
				&dbu.UpdatedAt,
				&dbu.DeletedAt,
				&dbu.Name,
				&dbu.Data,
				&dbu.Permissions,
			); err != nil {
				log.Println(err)
				return
			}

			chout <- user.User{
				ID:          dbu.ID,
				Name:        dbu.Name,
				Data:        dbu.Data,
				Permissions: dbu.Permissions,
			}
		}
	}()

	return chout, nil
}
