select title
from aws.aws_emr_security_configuration
where name = '{{ resourceName }}';