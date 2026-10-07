package service

import "context"

func (s *AccountTestService) BPSTicketGateway() *OpenAIGatewayService {
	if s == nil {
		return nil
	}
	return s.openaiGatewayService
}

type BPSTicketAccountView struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Status      string                 `json:"status"`
	Schedulable bool                   `json:"schedulable"`
	BPSEligible bool                   `json:"bps_eligible"`
	Config      BPSTicketAccountConfig `json:"config"`
	State       BPSTicketAccountState  `json:"state"`
	Tickets     []CodexTicketStatus    `json:"tickets"`
}

func (s *OpenAIGatewayService) BPSTicketAccounts(ctx context.Context) ([]BPSTicketAccountView, error) {
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, err
	}
	out := []BPSTicketAccountView{}
	for i := range accounts {
		a := &accounts[i]
		if !bpsTicketEligible(a) {
			continue
		}
		out = append(out, BPSTicketAccountView{ID: a.ID, Name: a.Name, Status: a.Status, Schedulable: a.Schedulable, BPSEligible: bpsEligible(a), Config: a.BPSTicketConfig(), State: a.BPSTicketState(), Tickets: s.BPSTicketStatuses(a)})
	}
	return out, nil
}
