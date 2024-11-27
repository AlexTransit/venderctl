package cashless_tinkoff

const (
	StatusNew              = "NEW" // Создан
	StatusNewI             = 1
	StatusFormShowed       = "FORMSHOWED" // Платежная форма открыта покупателем
	StatusFormShowedI      = 2
	StatusDeadlineExpired  = "DEADLINE_EXPIRED" // Просрочен
	StatusDeadlineExpiredI = 3
	StatusCanceled         = "CANCELED" // Отменен
	StatusCanceledI        = 4
	StatusPreauthorizing   = "PREAUTHORIZING" // Проверка платежных данных
	StatusPreauthorizingI  = 5
	StatusAuthorizing      = "AUTHORIZING" // Резервируется
	StatusAuthorizingI     = 6
	StatusAuthorized       = "AUTHORIZED" // Зарезервирован
	StatusAuthorizedI      = 7
	StatusAuthFail         = "AUTH_FAIL" // Не прошел авторизацию
	StatusAuthFailI        = 8
	StatusRejected         = "REJECTED" // Отклонен
	StatusRejectedI        = 9
	Status3DSChecking      = "3DS_CHECKING" // Проверяется по протоколу 3-D Secure
	Status3DSCheckingI     = 10
	Status3DSChecked       = "3DS_CHECKED" // Проверен по протоколу 3-D Secure
	Status3DSCheckedI      = 11
	StatusReversing        = "REVERSING" // Резервирование отменяется
	StatusReversingI       = 12
	StatusReversed         = "REVERSED" // Резервирование отменено
	StatusReversedI        = 13
	StatusConfirming       = "CONFIRMING" // Подтверждается
	StatusConfirmingI      = 14
	StatusConfirmed        = "CONFIRMED" // Подтвержден
	StatusConfirmedI       = 15
	StatusRefunding        = "REFUNDING" // Возвращается
	StatusRefundingI       = 16
	StatusQRRefunding      = "ASYNC_REFUNDING" // Возврат QR
	StatusQRRefundingI     = 17
	StatusPartialRefunded  = "PARTIAL_REFUNDED" // Возвращен частично
	StatusPartialRefundedI = 18
	StatusRefunded         = "REFUNDED" // Возвращен полностью
	StatusRefundedI        = 19
)
