# coding: utf-8

from __future__ import absolute_import
from datetime import date, datetime  # noqa: F401

from typing import List, Dict  # noqa: F401

from rest.models.base_model_ import Model
from rest import util


class HostTemplate(Model):
    """NOTE: This class is auto generated by OpenAPI Generator (https://openapi-generator.tech).

    Do not edit the class manually.
    """

    def __init__(self, id=None, name=None, cores=None, ram=None, disk=None, gpu_count=None, gpu_type=None):  # noqa: E501
        """HostTemplate - a model defined in OpenAPI

        :param id: The id of this HostTemplate.  # noqa: E501
        :type id: str
        :param name: The name of this HostTemplate.  # noqa: E501
        :type name: str
        :param cores: The cores of this HostTemplate.  # noqa: E501
        :type cores: int
        :param ram: The ram of this HostTemplate.  # noqa: E501
        :type ram: int
        :param disk: The disk of this HostTemplate.  # noqa: E501
        :type disk: int
        :param gpu_count: The gpu_count of this HostTemplate.  # noqa: E501
        :type gpu_count: int
        :param gpu_type: The gpu_type of this HostTemplate.  # noqa: E501
        :type gpu_type: str
        """
        self.openapi_types = {
            'id': str,
            'name': str,
            'cores': int,
            'ram': int,
            'disk': int,
            'gpu_count': int,
            'gpu_type': str
        }

        self.attribute_map = {
            'id': 'id',
            'name': 'name',
            'cores': 'cores',
            'ram': 'ram',
            'disk': 'disk',
            'gpu_count': 'gpuCount',
            'gpu_type': 'gpuType'
        }

        self._id = id
        self._name = name
        self._cores = cores
        self._ram = ram
        self._disk = disk
        self._gpu_count = gpu_count
        self._gpu_type = gpu_type

    @classmethod
    def from_dict(cls, dikt) -> 'HostTemplate':
        """Returns the dict as a model

        :param dikt: A dict.
        :type: dict
        :return: The HostTemplate of this HostTemplate.  # noqa: E501
        :rtype: HostTemplate
        """
        return util.deserialize_model(dikt, cls)

    @property
    def id(self):
        """Gets the id of this HostTemplate.


        :return: The id of this HostTemplate.
        :rtype: str
        """
        return self._id

    @id.setter
    def id(self, id):
        """Sets the id of this HostTemplate.


        :param id: The id of this HostTemplate.
        :type id: str
        """

        self._id = id

    @property
    def name(self):
        """Gets the name of this HostTemplate.


        :return: The name of this HostTemplate.
        :rtype: str
        """
        return self._name

    @name.setter
    def name(self, name):
        """Sets the name of this HostTemplate.


        :param name: The name of this HostTemplate.
        :type name: str
        """

        self._name = name

    @property
    def cores(self):
        """Gets the cores of this HostTemplate.


        :return: The cores of this HostTemplate.
        :rtype: int
        """
        return self._cores

    @cores.setter
    def cores(self, cores):
        """Sets the cores of this HostTemplate.


        :param cores: The cores of this HostTemplate.
        :type cores: int
        """

        self._cores = cores

    @property
    def ram(self):
        """Gets the ram of this HostTemplate.


        :return: The ram of this HostTemplate.
        :rtype: int
        """
        return self._ram

    @ram.setter
    def ram(self, ram):
        """Sets the ram of this HostTemplate.


        :param ram: The ram of this HostTemplate.
        :type ram: int
        """

        self._ram = ram

    @property
    def disk(self):
        """Gets the disk of this HostTemplate.


        :return: The disk of this HostTemplate.
        :rtype: int
        """
        return self._disk

    @disk.setter
    def disk(self, disk):
        """Sets the disk of this HostTemplate.


        :param disk: The disk of this HostTemplate.
        :type disk: int
        """

        self._disk = disk

    @property
    def gpu_count(self):
        """Gets the gpu_count of this HostTemplate.


        :return: The gpu_count of this HostTemplate.
        :rtype: int
        """
        return self._gpu_count

    @gpu_count.setter
    def gpu_count(self, gpu_count):
        """Sets the gpu_count of this HostTemplate.


        :param gpu_count: The gpu_count of this HostTemplate.
        :type gpu_count: int
        """

        self._gpu_count = gpu_count

    @property
    def gpu_type(self):
        """Gets the gpu_type of this HostTemplate.


        :return: The gpu_type of this HostTemplate.
        :rtype: str
        """
        return self._gpu_type

    @gpu_type.setter
    def gpu_type(self, gpu_type):
        """Sets the gpu_type of this HostTemplate.


        :param gpu_type: The gpu_type of this HostTemplate.
        :type gpu_type: str
        """

        self._gpu_type = gpu_type