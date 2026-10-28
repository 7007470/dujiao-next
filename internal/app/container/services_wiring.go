package container

// wireServiceDependencies 收口构造后才能建立的双向或延迟依赖。
func (c *Container) wireServiceDependencies() {
	c.UserAuthService.SetMemberLevelService(c.MemberLevelService)
	c.OrderRefundService.SetResellerAccounting(c.ResellerAccountingLedger)
	c.OrderRefundService.SetRefundGuard(c.BoostPointsService) // BOOST-EXT: 积分包订单退款前必须先撤回积分
	c.OrderService.SetRefundGuard(c.BoostPointsService)       // BOOST-EXT: 通用状态接口改"已退款"同样拦截
	c.PaymentService.SetMemberLevelService(c.MemberLevelService)
	c.PaymentService.SetProcurementService(c.ProcurementOrderService)
	c.PaymentService.SetDownstreamCallbackService(c.DownstreamCallbackService)
	c.FulfillmentService.SetDownstreamCallbackService(c.DownstreamCallbackService)
}
