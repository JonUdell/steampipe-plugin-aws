select
  member_account_id,
  email,
  administrator_id,
  member_status
from
  aws_securityhub_member
where
  member_status='Invited';