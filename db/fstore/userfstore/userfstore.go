package userfstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gbbackend1/reguser/app/repos/user"

	"github.com/google/uuid"
)

var _ user.UserStore = &UserFileStore{}

type Position int64

type SortedUserIndexRecords []UserIndexRecord

func (x SortedUserIndexRecords) Len() int           { return len(x) }
func (x SortedUserIndexRecords) Less(i, j int) bool { return x[i].Position < x[j].Position }
func (x SortedUserIndexRecords) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

type UserIndexRecord struct {
	UserID   uuid.UUID
	Position Position
	Delete   bool
}

type UserFileStore struct {
	sync.Mutex
	fdata   *os.File
	pkmap   map[uuid.UUID]Position
	idxRecs SortedUserIndexRecords
	pkchan  chan UserIndexRecord
	pk      *os.File
}

func NewUserFileStore(dir string) (*UserFileStore, error) {
	fdata, err := os.OpenFile(filepath.Join(dir, "fdata.dat"), os.O_RDWR|os.O_SYNC|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	pkmap := make(map[uuid.UUID]Position)
	idxRecs := make(SortedUserIndexRecords, 0, 1000)

	pk, err := os.OpenFile(filepath.Join(dir, "pk.dat"), os.O_RDONLY, 0644)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		var ir UserIndexRecord
		for {
			if err := binary.Read(pk, binary.LittleEndian, &ir); err == io.EOF {
				break
			}
			if ir.Delete {
				delete(pkmap, ir.UserID)
			} else {
				pkmap[ir.UserID] = ir.Position

				idx := sort.Search(len(idxRecs), func(i int) bool {
					return idxRecs[i].Position >= ir.Position
				})
				if idx == len(idxRecs) {
					// добавление
					idxRecs = append(idxRecs, ir)
				} else {
					if idxRecs[idx].Position == ir.Position {
						if !ir.Delete {
							// замена
							idxRecs[idx].UserID = ir.UserID
						} else {
							// удаление
							idxRecs = append(idxRecs[:idx], idxRecs[idx+1:]...)
						}
					} else {
						// вставка
						idxRecs = append(idxRecs[:idx], append(SortedUserIndexRecords{ir},
							idxRecs[idx+1:]...)...)
					}
				}

			}
		}
	}
	pk.Close()

	pk, err = os.OpenFile(filepath.Join(dir, "pk.dat"), os.O_WRONLY|os.O_SYNC|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	st := &UserFileStore{
		fdata:   fdata,
		pkmap:   pkmap,
		pk:      pk,
		pkchan:  make(chan UserIndexRecord, 100),
		idxRecs: idxRecs,
	}

	go st.writePK()

	return st, nil
}

func (st *UserFileStore) Close() {
	st.fdata.Close()
	st.pk.Close()
}

const DBFileUserLen = 16 + 8 + 1 + 250 + 2 + 1000 + 2

type DBFileUser struct {
	ID          [16]byte
	DeletedAt   [8]byte
	NameLen     [1]byte
	Name        [250]byte
	DataLen     [2]byte
	Data        [1000]byte
	Permissions [2]byte
}

func (st *UserFileStore) addUserToFdata(u user.User) (Position, error) {
	if len(u.Data) > 1000 {
		return -1, fmt.Errorf("data too much")
	}
	st.fdata.Seek(0, io.SeekEnd)
	fi, err := st.fdata.Stat()
	if err != nil {
		return -1, err
	}
	p := Position(fi.Size())

	ln := len(st.idxRecs)
	idx := sort.Search(ln, func(i int) bool {
		return st.idxRecs[i].Position >= p
	})
	st.idxRecs = append(st.idxRecs, UserIndexRecord{})
	if idx < ln {
		copy(st.idxRecs[idx+1:], st.idxRecs[idx:])
	}
	st.idxRecs[idx] = UserIndexRecord{
		UserID:   u.ID,
		Position: p,
		Delete:   false,
	}

	dbu := DBFileUser{
		ID:      u.ID,
		NameLen: [1]byte{byte(len(u.Name))},
	}
	binary.LittleEndian.PutUint16(dbu.DataLen[:], uint16(len(u.Data)))
	binary.LittleEndian.PutUint16(dbu.Permissions[:], uint16(u.Permissions))
	copy(dbu.Data[:], []byte(u.Data))
	copy(dbu.Name[:], []byte(u.Name))

	return p, binary.Write(st.fdata, binary.LittleEndian, dbu)
}

func (st *UserFileStore) writePK() {
	for v := range st.pkchan {
		if err := binary.Write(st.pk, binary.LittleEndian, v); err != nil {
			log.Println("writePK: ", err)
		}
	}
}

func (st *UserFileStore) addUserToFdataAndPK(u user.User) error {
	if _, ok := st.pkmap[u.ID]; ok {
		return fmt.Errorf("user duplicates")
	}
	p, err := st.addUserToFdata(u)
	if err != nil {
		return err
	}
	st.pkmap[u.ID] = p
	st.pkchan <- UserIndexRecord{
		UserID:   u.ID,
		Position: p,
	}
	return nil
}

func (us *UserFileStore) Create(ctx context.Context, u user.User) (*uuid.UUID, error) {
	us.Lock()
	defer us.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	uid := uuid.New()
	u.ID = uid
	err := us.addUserToFdataAndPK(u)
	if err != nil {
		return nil, err
	}
	return &uid, nil
}

func (st *UserFileStore) readDBFileUserByID(id uuid.UUID) (DBFileUser, error) {
	p, ok := st.pkmap[id]
	if !ok {
		return DBFileUser{}, sql.ErrNoRows
	}
	st.fdata.Seek(int64(p), io.SeekStart)
	dbu := DBFileUser{}
	if err := binary.Read(st.fdata, binary.LittleEndian, &dbu); err != nil {
		return dbu, err
	}
	return dbu, nil
}

func (st *UserFileStore) readUserByID(id uuid.UUID) (user.User, error) {
	dbu, err := st.readDBFileUserByID(id)
	if err != nil {
		return user.User{}, err
	}
	if dbu.DeletedAt != [8]byte{} {
		return user.User{}, sql.ErrNoRows
	}

	u := user.User{
		ID:          dbu.ID,
		Name:        string(dbu.Name[:dbu.NameLen[0]]),
		Data:        string(dbu.Data[:binary.LittleEndian.Uint16(dbu.DataLen[:])]),
		Permissions: int(binary.LittleEndian.Uint16(dbu.Permissions[:])),
	}
	return u, nil
}

func (us *UserFileStore) Read(ctx context.Context, uid uuid.UUID) (*user.User, error) {
	us.Lock()
	defer us.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	u, err := us.readUserByID(uid)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (st *UserFileStore) deleteDBFileUserByID(id uuid.UUID) error {
	p, ok := st.pkmap[id]
	if !ok {
		return nil
	}
	st.fdata.Seek(int64(p)+16, io.SeekStart)
	if err := binary.Write(st.fdata, binary.LittleEndian, time.Now().Unix()); err != nil {
		return err
	}
	delete(st.pkmap, id)
	st.pkchan <- UserIndexRecord{
		UserID: id,
		Delete: true,
	}
	return nil
}

// не возвращает ошибку если не нашли
func (us *UserFileStore) Delete(ctx context.Context, uid uuid.UUID) error {
	us.Lock()
	defer us.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return us.deleteDBFileUserByID(uid)
}

func (us *UserFileStore) iterateInFdata(ctx context.Context, s string) chan DBFileUser {
	chout := make(chan DBFileUser, 100)
	bs := []byte(s)
	us.fdata.Seek(0, io.SeekStart)
	go func() {
		defer close(chout)
		var buf [DBFileUserLen * 100]byte
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := us.fdata.Read(buf[:])
			if err == io.EOF {
				return
			}
			if err != nil {
				log.Println(err)
				return
			}
			b := buf[:n]

			// TODO: p0:=

			for len(b) > 0 {
				pidx := bytes.Index(b, bs)
				if pidx < 0 {
					continue
				}
				// TODO: проверяем что мы попали в границы поля Name

				var uir UserIndexRecord
				idx := sort.Search(len(us.idxRecs), func(i int) bool {
					return us.idxRecs[i].Position >= Position(pidx)
				})
				if idx == len(us.idxRecs) {
					uir = us.idxRecs[idx]
				} else {
					uir = us.idxRecs[idx-1]
				}

				// TODO:
				b = b[uir.Position-0:]
				// TODO: заменить на срез буфера со следующей записи, продолжить цикл если буфер пуст

				us.fdata.Seek(int64(uir.Position+DBFileUserLen), io.SeekStart)

				dbu := DBFileUser{}
				if err := binary.Read(us.fdata, binary.LittleEndian, &dbu); err != nil {
					log.Println(err)
					return
				}

				// TODO:
				// us.fdata.Seek()

				if dbu.DeletedAt != [8]byte{} {
					continue
				}
				chout <- dbu
			}
		}
	}()

	return chout
}

func (us *UserFileStore) searchByName(ctx context.Context, s string, chout chan user.User) {
	defer close(chout)
	us.Lock()
	defer us.Unlock()

	chin := us.iterateInFdata(ctx, s)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
			return
		case dbu, ok := <-chin:
			if !ok {
				return
			}
			if dbu.DeletedAt != [8]byte{} {
				continue
			}

			u := user.User{
				ID:          dbu.ID,
				Name:        string(dbu.Name[:dbu.NameLen[0]]),
				Data:        string(dbu.Data[:binary.LittleEndian.Uint16(dbu.DataLen[:])]),
				Permissions: int(binary.LittleEndian.Uint16(dbu.Permissions[:])),
			}

			chout <- u
		}
	}
}

func (us *UserFileStore) SearchUsers(ctx context.Context, s string) (chan user.User, error) {
	us.Lock()
	defer us.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	chout := make(chan user.User, 100)

	go us.searchByName(ctx, s, chout)

	return chout, nil
}
