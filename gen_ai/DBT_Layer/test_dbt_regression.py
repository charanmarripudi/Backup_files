import asyncio
import logging
import sys
import os

# Add the project root to sys.path to resolve orchestrator
sys.path.append("/Users/algofusion/Documents/dnc_backend_v2")

from orchestrator.gen_ai.DBT_Layer.semantic_orchestrator import DBTSemanticOrchestrator

# Configure Logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s: %(message)s')

async def run_tests():
    orchestrator = DBTSemanticOrchestrator()
    
    test_questions = [
        "Show vehicles where violations reduced compared to last month",
        "Top 5 locations with maximum violations",
        "Show alerts where no action has been taken",
        "Any vehicles with violations but zero alerts raised"
    ]
    
    print("\n" + "="*50)
    print("DBT REGRESSION TEST (COMPOSITE FIX PHASE)")
    print("="*50)

    for q in test_questions:
        print(f"\n[USER QUESTION]: {q}")
        try:
            result = await orchestrator.generate_query(q)
            print(f"[SOURCE]: {result.get('source')}")
            print(f"[GENERATED SQL]:\n{result.get('sql_query')}")
        except Exception as e:
            print(f"[ERROR]: {str(e)}")
        print("-" * 50)

if __name__ == "__main__":
    asyncio.run(run_tests())
