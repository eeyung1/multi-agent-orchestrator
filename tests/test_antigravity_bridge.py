import unittest
from integrations.antigravity.bridge import BridgeError, parse_json_object, validate_proposal

class AntigravityBridgeTests(unittest.TestCase):
    def setUp(self):
        self.request = {
            "allowed_scopes": ["src", "docs"],
            "capabilities": ["coding"],
            "validators": ["go-test", "diff-check"],
            "required_validators": ["diff-check"],
        }

    def test_parses_json_object(self):
        self.assertEqual(parse_json_object('{"questions":["Need scope"]}'),
                         {"questions": ["Need scope"]})

    def test_accepts_clarification_questions(self):
        self.assertEqual(validate_proposal({"questions": ["Which API?"]}, self.request),
                         {"questions": ["Which API?"]})

    def test_normalizes_valid_plan(self):
        proposal = validate_proposal({
            "plan": {"objective": "Add one feature", "tasks": [{
                "id": "task-1", "feature": "Feature", "scope": "src/feature",
                "description": "Implement the feature", "depends_on": [],
                "acceptance_criteria": ["A test passes"],
                "validation": ["go-test", "diff-check"], "capability": "coding",
            }]}
        }, self.request)
        task = proposal["plan"]["tasks"][0]
        self.assertEqual(task["scope"], "src/feature")
        self.assertEqual(task["validation"], ["go-test", "diff-check"])
        self.assertTrue(proposal["plan"]["id"].startswith("antigravity-"))

    def test_rejects_scope_outside_allowlist(self):
        with self.assertRaises(BridgeError):
            validate_proposal({"plan": {"objective": "Bad scope", "tasks": [{
                "id": "task-1", "feature": "Feature", "scope": "../outside",
                "description": "Do not allow this", "validation": ["diff-check"],
            }]}}, self.request)

    def test_rejects_unknown_validator(self):
        with self.assertRaises(BridgeError):
            validate_proposal({"plan": {"objective": "Bad validator", "tasks": [{
                "id": "task-1", "feature": "Feature", "scope": "src/feature",
                "description": "Do not allow this", "validation": ["invented-check"],
            }]}}, self.request)

    def test_rejects_duplicate_task_ids(self):
        task = {"id": "task-1", "feature": "Feature", "scope": "src/feature",
                "description": "Implement", "validation": ["diff-check"]}
        with self.assertRaises(BridgeError):
            validate_proposal({"plan": {"objective": "Duplicate", "tasks": [task, dict(task)]}},
                              self.request)

    def test_rejects_non_object_json(self):
        with self.assertRaises(BridgeError):
            parse_json_object('["not", "an object"]')

if __name__ == "__main__":
    unittest.main()
