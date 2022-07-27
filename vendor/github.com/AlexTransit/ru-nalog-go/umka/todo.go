package umka

import (
	ru_nalog "github.com/AlexTransit/ru-nalog-go"
	"github.com/juju/errors"
)

func (u *Umka) CalcReport() (*ru_nalog.Doc, error) {
	return nil, errors.NotImplementedf("calc-report")
}

func (u *Umka) XReport() (*ru_nalog.Doc, error) {
	return nil, errors.NotImplementedf("x-report")
}

func (u *Umka) Fiscalize(sessionId string, d *ru_nalog.Doc) (*ru_nalog.Doc, error) {
	return nil, errors.NotImplementedf("fiscalize")
}

func (u *Umka) Danger_CloseFiscalStorage(sessionId string) (*ru_nalog.Doc, error) {
	return nil, errors.NotImplementedf("danger_closefiscalstorage")
}
