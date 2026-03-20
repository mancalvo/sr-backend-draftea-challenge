package workflows

const (
	DepositChargeStep                = "deposit_charge"
	DepositCreditStep                = "deposit_credit"
	DepositCompletedStep             = "deposit_completed"
	DepositChargeFailedStep          = "deposit_charge_failed"
	PurchaseDebitStep                = "purchase_debit"
	PurchaseGrantStep                = "purchase_grant"
	PurchaseCompletedStep            = "purchase_completed"
	PurchaseDebitRejectedStep        = "purchase_debit_rejected"
	PurchaseCompensationCreditStep   = "purchase_compensation_credit"
	PurchaseCompensationCreditedStep = "purchase_compensation_credited"
	RefundRevokeAccessStep           = "refund_revoke_access"
	RefundCreditStep                 = "refund_credit"
	RefundCompletedStep              = "refund_completed"
	RefundRevokeRejectedStep         = "refund_revoke_rejected"
)
