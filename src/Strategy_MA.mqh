//+------------------------------------------------------------------+
//|                                                 Strategy_MA.mqh  |
//|                               Copyright 2026, google.com.jules   |
//|                    Moving Average Crossover Strategy Module      |
//+------------------------------------------------------------------+
#property copyright "Copyright 2026, google.com.jules"
#property strict

#include <Trade\Trade.mqh>

class Strategy_MA
{
private:
   CTrade  m_trade;
   int     m_handleFastMA;
   int     m_handleSlowMA;
   double  m_lotSize;
   int     m_stopLoss;
   int     m_takeProfit;
   ulong   m_magicNumber;

public:
   Strategy_MA() : m_handleFastMA(INVALID_HANDLE), m_handleSlowMA(INVALID_HANDLE),
                   m_lotSize(0.01), m_stopLoss(150), m_takeProfit(300), m_magicNumber(3637101) {}

   ~Strategy_MA()
   {
      if(m_handleFastMA != INVALID_HANDLE) IndicatorRelease(m_handleFastMA);
      if(m_handleSlowMA != INVALID_HANDLE) IndicatorRelease(m_handleSlowMA);
   }

   void Init(double lotSize = 0.01, int sl = 150, int tp = 300, ulong magic = 3637101)
   {
      m_lotSize = lotSize;
      m_stopLoss = sl;
      m_takeProfit = tp;
      m_magicNumber = magic;
      m_trade.SetExpertMagicNumber(m_magicNumber);

      m_handleFastMA = iMA(_Symbol, _Period, 10, 0, MODE_EMA, PRICE_CLOSE);
      m_handleSlowMA = iMA(_Symbol, _Period, 20, 0, MODE_EMA, PRICE_CLOSE);
   }

   void Run()
   {
      if(m_handleFastMA == INVALID_HANDLE || m_handleSlowMA == INVALID_HANDLE) return;

      static datetime lastBarTime = 0;
      datetime currentBarTime = iTime(_Symbol, _Period, 0);
      if(currentBarTime == lastBarTime) return;
      lastBarTime = currentBarTime;

      // Count open positions for this strategy
      int totalPositions = 0;
      for(int i = PositionsTotal() - 1; i >= 0; i--)
      {
         if(PositionGetSymbol(i) == _Symbol && PositionGetInteger(POSITION_MAGIC) == m_magicNumber)
            totalPositions++;
      }
      if(totalPositions > 0) return;

      double fastMA[2], slowMA[2];
      if(CopyBuffer(m_handleFastMA, 0, 1, 2, fastMA) < 2) return;
      if(CopyBuffer(m_handleSlowMA, 0, 1, 2, slowMA) < 2) return;

      if(fastMA[0] <= slowMA[0] && fastMA[1] > slowMA[1])
      {
         double ask = SymbolInfoDouble(_Symbol, SYMBOL_ASK);
         double sl  = (m_stopLoss > 0) ? ask - m_stopLoss * _Point : 0;
         double tp  = (m_takeProfit > 0) ? ask + m_takeProfit * _Point : 0;
         m_trade.Buy(m_lotSize, _Symbol, ask, sl, tp, "Strategy_MA Buy");
      }
      else if(fastMA[0] >= slowMA[0] && fastMA[1] < slowMA[1])
      {
         double bid = SymbolInfoDouble(_Symbol, SYMBOL_BID);
         double sl  = (m_stopLoss > 0) ? bid + m_stopLoss * _Point : 0;
         double tp  = (m_takeProfit > 0) ? bid - m_takeProfit * _Point : 0;
         m_trade.Sell(m_lotSize, _Symbol, bid, sl, tp, "Strategy_MA Sell");
      }
   }
};
