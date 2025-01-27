package cashless_tinkoff

import "github.com/nikita-vanyasin/tinkoff"

// --стадии платежа --
// Отправлен				- StatusNew
// Платежная форма открыта	- StatusFormShowed
// Резервируется			- StatusAuthorizing
// Зарезервирован			- StatusAuthorized
// Подтверждается			- StatusConfirming
// Подтвержден				- StatusConfirmed		- credited
// -- если идет отмена --
// Возвращается				- StatusRefunding
// Возвращен полностью		- StatusRefunded		- credited=0

var bankOrderState = map[string]int32{
	tinkoff.StatusNew:             1,  // Создан
	tinkoff.StatusFormShowed:      2,  // Платежная форма открыта покупателем
	tinkoff.StatusDeadlineExpired: 3,  // Просрочен
	tinkoff.StatusCanceled:        4,  // Отменен
	tinkoff.StatusAuthorizing:     6,  // Резервируется
	tinkoff.StatusAuthorized:      7,  // Зарезервирован
	tinkoff.StatusAuthFail:        8,  // Не прошел авторизацию
	tinkoff.StatusRejected:        9,  // Отклонен
	tinkoff.Status3DSChecking:     10, // Проверяется по протоколу 3-D Secure
	tinkoff.Status3DSChecked:      11, // Проверен по протоколу 3-D Secure
	tinkoff.StatusReversing:       12, // Резервирование отменяется
	tinkoff.StatusReversed:        13, // Резервирование отменено
	tinkoff.StatusConfirming:      14, // Подтверждается
	tinkoff.StatusConfirmed:       15, // Подтвержден
	tinkoff.StatusRefunding:       16, // Возвращается
	tinkoff.StatusQRRefunding:     17, // Возврат QR
	tinkoff.StatusPartialRefunded: 18, // Возвращен частично
	tinkoff.StatusRefunded:        19, // Возвращен полностью
}

func getBankOrderStatusName(stateIndex int32) string {
	for k, v := range bankOrderState {
		if v == stateIndex {
			return k
		}
	}
	return "unknow"
}
func getBankOrderStatusIndex(stateName string) int32 {
	// index = bankOrderState[stateName]
	// if index == 0 {
	// 	// CashLess.g.Log.Errorf("undefined bank state (%s)", stateName)
	// }
	return bankOrderState[stateName]
}
