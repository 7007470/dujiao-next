package consumer

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dujiao-next/internal/logger"
	"github.com/dujiao-next/internal/queue"

	"github.com/hibiken/asynq"
)

// handleNotificationDispatch 处理通知中心异步分发任务。
func (c *Consumer) handleNotificationDispatch(ctx context.Context, task *asynq.Task) error {
	if c == nil || task == nil {
		logger.Debugw("worker_notification_dispatch_skip_nil", "consumer_nil", c == nil, "task_nil", task == nil)
		return nil
	}
	if c.NotificationService == nil {
		logger.Warnw("worker_notification_dispatch_skip_service_nil")
		return nil
	}
	var payload queue.NotificationDispatchPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		logger.Warnw("worker_notification_dispatch_unmarshal_failed", "error", err)
		return err
	}
	if strings.TrimSpace(payload.EventType) == "" {
		logger.Debugw("worker_notification_dispatch_skip_empty_event")
		return nil
	}
	if err := c.NotificationService.Dispatch(ctx, payload); err != nil {
		logger.Warnw("worker_notification_dispatch_failed",
			"event_type", payload.EventType,
			"biz_type", payload.BizType,
			"biz_id", payload.BizID,
			"error", err,
		)
		return err
	}
	return nil
}

// handleAffiliateConfirmCommissions 处理分销佣金确认任务。
func (c *Consumer) handleAffiliateConfirmCommissions(_ context.Context, _ *asynq.Task) error {
	if c == nil || c.AffiliateService == nil {
		logger.Debugw("worker_affiliate_confirm_skip_nil", "consumer_nil", c == nil)
		return nil
	}
	if err := c.AffiliateService.ConfirmDueCommissions(time.Now()); err != nil {
		logger.Warnw("worker_affiliate_confirm_due_failed", "error", err)
		return err
	}
	return nil
}

func (c *Consumer) handleResellerConfirmLedger(_ context.Context, _ *asynq.Task) error {
	if c == nil || c.ResellerAccountingLedger == nil {
		logger.Debugw("worker_reseller_confirm_ledger_skip_nil", "consumer_nil", c == nil)
		return nil
	}
	affected, err := c.ResellerAccountingLedger.ConfirmDueLedgerEntries(time.Now())
	if err != nil {
		logger.Warnw("worker_reseller_confirm_ledger_failed", "error", err)
		return err
	}
	logger.Debugw("worker_reseller_confirm_ledger_ok", "affected", affected)
	return nil
}

// handleBoostPointsMaintain BOOST-EXT：积分批次到期失效；补发支付成功但没到账的积分包订单；代练任务 / 绑定超时回收。
func (c *Consumer) handleBoostPointsMaintain(_ context.Context, _ *asynq.Task) error {
	if c == nil || c.BoostPointsService == nil {
		return nil
	}
	expired, err := c.BoostPointsService.ExpireDue(500)
	if err != nil {
		logger.Warnw("worker_boost_points_expire_failed", "error", err)
	}
	granted, grantErr := c.BoostPointsService.RetryUngranted(2*time.Minute, 100)
	if grantErr != nil {
		logger.Warnw("worker_boost_points_retry_grant_failed", "error", grantErr)
	}
	reaped := 0
	var reapErr error
	if c.BoostTaskService != nil {
		// 代练任务 / 绑定：等待输入超时、任务超过时限
		reaped, reapErr = c.BoostTaskService.Reap(200)
		if reapErr != nil {
			logger.Warnw("worker_boost_task_reap_failed", "error", reapErr)
		}
	}
	if expired > 0 || granted > 0 || reaped > 0 {
		logger.Infow("worker_boost_points_maintain_ok", "expired_lots", expired, "granted_orders", granted, "reaped", reaped)
	}
	if err != nil {
		return err
	}
	if grantErr != nil {
		return grantErr
	}
	return reapErr
}

// handleReconciliationRun 处理对账任务执行。
func (c *Consumer) handleReconciliationRun(ctx context.Context, task *asynq.Task) error {
	if c == nil || task == nil || c.ReconciliationService == nil {
		logger.Debugw("worker_reconciliation_run_skip_nil")
		return nil
	}
	var payload queue.ReconciliationRunPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		logger.Warnw("worker_reconciliation_run_unmarshal_failed", "error", err)
		return err
	}
	if payload.JobID == 0 {
		return nil
	}
	if err := c.ReconciliationService.Execute(ctx, payload.JobID); err != nil {
		logger.Warnw("worker_reconciliation_run_failed",
			"job_id", payload.JobID,
			"error", err,
		)
		return err
	}
	return nil
}
