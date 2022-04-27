package tinkoff

const (
	TaxationOSN              = "osn"                // общая СН
	TaxationUSNIncome        = "usn_income"         // упрощенная СН (доходы)
	TaxationUSNIncomeOutcome = "usn_income_outcome" // упрощенная СН (доходы минус расходы)
	TaxationENVD             = "envd"               // единый налог на вмененный доход
	TaxationESN              = "esn"                // единый сельскохозяйственный налог
	TaxationPatent           = "patent"             // патентная СН
)

const (
	VATNone = "none"   // без НДС
	VAT0    = "vat0"   // НДС по ставке 0%
	VAT10   = "vat10"  // НДС чека по ставке 10%
	VAT110  = "vat110" // НДС чека по расчетной ставке 10/110

	// Deprecated: since 1 Jan 2019 vat18 will be replaced automatically to vat20 on the side of Tinkoff bank. Use VAT20 instead
	VAT18 = "vat18" // НДС чека по ставке 18%
	VAT20 = "vat20" // НДС чека по ставке 20%

	// Deprecated: since 1 Jan 2019 vat118 will be replaced automatically to vat120 on the side of Tinkoff bank. Use VAT120 instead
	VAT118 = "vat118" // НДС чека по расчетной ставке 18/118
	VAT120 = "vat120" // НДС чека по расчетной ставке 20/120
)

const (
	PaymentMethodFullPayment    = "full_payment"    // полный расчет
	PaymentMethodFullPrepayment = "full_prepayment" // предоплата 100%
	PaymentMethodPrepayment     = "prepayment"      // предоплата
	PaymentMethodAdvance        = "advance"         // аванс
	PaymentMethodPartialPayment = "partial_payment" // частичный расчет и кредит
	PaymentMethodCredit         = "credit"          // передача в кредит
	PaymentMethodCreditPayment  = "credit_payment"  // оплата кредита
)

const (
	PaymentObjectCommodity            = "commodity"             // товар
	PaymentObjectExcise               = "excise"                // подакцизный товар
	PaymentObjectJob                  = "job"                   // работа
	PaymentObjectService              = "service"               // услуга
	PaymentObjectGamblingBet          = "gambling_bet"          // ставка азартной игры
	PaymentObjectGamblingPrize        = "gambling_prize"        // выигрыш азартной игры
	PaymentObjectLottery              = "lottery"               // лотерейный билет
	PaymentObjectLotteryPrize         = "lottery_prize"         // выигрыш лотереи
	PaymentObjectIntellectualActivity = "intellectual_activity" // предоставление результатов интеллектуальной деятельности
	PaymentObjectPayment              = "payment"               // платеж
	PaymentObjectAgentCommission      = "agent_commission"      // агентское вознаграждение
	PaymentObjectComposite            = "composite"             // составной предмет расчета
	PaymentObjectAnother              = "another"               // иной предмет расчета
)

type Receipt struct {
	Email        string           `json:"Email,omitempty"`        // Электронная почта покупателя
	Phone        string           `json:"Phone,omitempty"`        // Контактный телефон покупателя
	EmailCompany string           `json:"EmailCompany,omitempty"` // Электронная почта продавца
	Taxation     string           `json:"Taxation"`               // Система налогооблажения. см. константы Taxation*
	Items        []*ReceiptItem   `json:"Items"`                  // Массив позиций чека с информацией о товарах.
	Payments     *ReceiptPayments `json:"Payments,omitempty"`     // Объект с информацией о видах оплаты заказа.
}

type ReceiptItem struct {
	Name          string `json:"Name"`                    // Наименование товара
	Quantity      string `json:"Quantity"`                // Количество или вес товара
	Amount        uint64 `json:"Amount"`                  // Стоимость товара в копейках
	Price         uint64 `json:"Price"`                   // Цена товара в копейках
	PaymentMethod string `json:"PaymentMethod,omitempty"` // Признак способа расчета. см. PaymentMethod*
	PaymentObject string `json:"PaymentObject,omitempty"` // Признак предмета расчета. см. PaymentObject*
	Tax           string `json:"Tax"`                     // Ставка налога. см. константы VAT*
	Ean13         string `json:"Ean13,omitempty"`         // Ean13
	ShopCode      string `json:"ShopCode,omitempty"`      // Код магазина
}

type ReceiptPayments struct {
	Cash           uint64 `json:"Cash,omitempty"`           // Вид оплаты "Наличные". Сумма к оплате в копейках не более 14 знаков
	Electronic     uint64 `json:"Electronic"`               // Вид оплаты "Безналичный".
	AdvancePayment uint64 `json:"AdvancePayment,omitempty"` // Вид оплаты "Предварительная оплата (Аванс)".
	Credit         uint64 `json:"Credit,omitempty"`         // Вид оплаты "Постоплата (Кредит)"
	Provision      uint64 `json:"Provision,omitempty"`      // Вид оплаты "Иная форма оплаты".
}
