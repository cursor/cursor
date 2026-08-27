//+------------------------------------------------------------------+
//|                                                  RiskManager.mqh |
//|                               Copyright 2026, google.com.jules   |
//|                          Shared Risk Management Utility for EAs  |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property strict

class RiskManager
{
private:
   double m_maxDrawdownPct;
   double m_maxDailyLoss;

public:
   RiskManager() : m_maxDrawdownPct(10.0), m_maxDailyLoss(100.0) {}

   void Init(double maxDrawdownPct = 10.0, double maxDailyLoss = 100.0)
   {
      m_maxDrawdownPct = maxDrawdownPct;
      m_maxDailyLoss = maxDailyLoss;
   }

   bool IsTradeAllowed()
   {
      double balance = AccountInfoDouble(ACCOUNT_BALANCE);
      double equity = AccountInfoDouble(ACCOUNT_EQUITY);

      if(balance <= 0) return false;

      double currentDrawdownPct = ((balance - equity) / balance) * 100.0;
      if(currentDrawdownPct >= m_maxDrawdownPct)
      {
         Print("RiskManager Alert: Max drawdown percentage exceeded (", currentDrawdownPct, "% >= ", m_maxDrawdownPct, "%)");
         return false;
      }
      return true;
   }
};
